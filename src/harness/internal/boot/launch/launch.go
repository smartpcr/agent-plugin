// Package launch holds the facts fixed before the component tree exists
// (DH-D-BOOT-2, DH-ADR-BOOT-4): the launch data (argv, working directory, OS, user,
// build identity, home, profile), the launch environment, and the expression scope
// those facts give to internal/boot/expr. Build is a pure function of the
// launcher's Request; Probe is the one place that reads the process for it.
//
// The snapshot is never written into the process environment, and a file found in
// the working directory or the home cannot set a name that changes how the harness
// starts.
package launch

import (
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/homedir"
)

// Reasons of the errors Build returns.
const (
	// ReasonForbiddenName: a discovered file sets a launch-only name, or a proxy or
	// endpoint name outside the home file. Boot exits 78 and applies nothing.
	ReasonForbiddenName herr.Reason = "launch/forbidden-name"
	// ReasonBadFile: a .env file does not parse.
	ReasonBadFile herr.Reason = "launch/bad-env-file"
	// ReasonRead: a .env file exists but cannot be read.
	ReasonRead herr.Reason = "launch/read"
	// ReasonFacts: a launch fact (the working directory) is missing or unusable.
	ReasonFacts herr.Reason = "launch/facts"
)

// DevelVersion is the version of a build without module version information.
const DevelVersion = "(devel)"

// ProjectEnvFile is the name of the working directory's environment file.
const ProjectEnvFile = ".env"

// HomeEnvFile is the name of the home's environment file, under home.Layout.Config.
const HomeEnvFile = ".env"

// BuildIdentity says which build is running.
type BuildIdentity struct {
	// Version is the module version, or DevelVersion.
	Version string
	// Revision is the VCS revision the binary was built from, when known.
	Revision string
	// Time is the VCS commit time (RFC 3339), when known.
	Time string
	// Modified reports a build from a working tree with uncommitted changes.
	Modified bool
	// GoVersion is the toolchain that built the binary.
	GoVersion string
}

// Data are the launch facts. Every component and expression reads the same frozen
// value; the slices are copies owned by the Data.
type Data struct {
	// Profile is the profile's name.
	Profile string
	// Home is the absolute harness home.
	Home string
	// Cwd is the absolute working directory.
	Cwd string
	// Version is the build's version (Build.Version, or DevelVersion).
	Version string
	// OS is the platform: "windows", "linux", "darwin", ...
	OS string
	// User is the name of the user the process runs as; empty when unknown.
	User string
	// Build is the build identity.
	Build BuildIdentity
	// Args is the full argument vector, the program first.
	Args []string
	// TTY reports an interactive terminal.
	TTY bool
	// Safe is --safe (DH-ADR-BOOT-12).
	Safe bool
	// Overlays are the files named by --layer, in order.
	Overlays []string
	// Words are the app's own words, parsed by the app's start-up component; they
	// never reach expressions.
	Words []string
}

// Clone returns a copy of d that shares no slice with it.
func (d Data) Clone() Data {
	d.Args = slices.Clone(d.Args)
	d.Overlays = slices.Clone(d.Overlays)
	d.Words = slices.Clone(d.Words)
	return d
}

// ReadFile reads one file; a missing file is reported with an error that satisfies
// errors.Is(err, fs.ErrNotExist).
type ReadFile func(path string) ([]byte, error)

// Request is what the launcher hands to Build. Probe fills the parts that come from
// the process; a test builds it in memory.
type Request struct {
	// Args is the full argument vector, the program first.
	Args []string
	// Cwd is the working directory; a relative path is made absolute.
	Cwd string
	// OS is the platform; empty means runtime.GOOS.
	OS string
	// User is the user's name.
	User string
	// Build is the build identity.
	Build BuildIdentity
	// Environ is the process environment as "NAME=value" entries.
	Environ []string
	// Home is the --home option; empty falls back to DSH_HOME, then homedir.Default.
	Home string
	// Profile is the profile's name.
	Profile string
	// TTY reports an interactive terminal.
	TTY bool
	// Safe is --safe.
	Safe bool
	// Overlays are the --layer files.
	Overlays []string
	// Words are the app's words.
	Words []string
	// SystemProxy is the operating system's static proxy setting (A23's sysproxy),
	// read once by the launcher; only proxy names are taken from it.
	SystemProxy map[string]string
	// ReadFile reads the two .env files; nil reads none.
	ReadFile ReadFile
	// CheckPrivate verifies that a path is private to its owner (Probe sets
	// home.CheckPrivate). When the home's config/.env exists, Build runs it on
	// config/ and on the file before trusting the file's proxy, endpoint and TLS
	// trust names; a failure fails the build. Nil skips the check, which only an
	// in-memory ReadFile should rely on.
	CheckPrivate func(path string) error
}

// Build turns a request into the launch data and the launch environment: the home
// is resolved (--home, then DSH_HOME of the process, then the default), then the
// working directory's .env and the home's config/.env are read. A file that sets a
// launch-only name fails the whole build, naming the file and the variable; nothing
// is applied.
func Build(req Request) (Data, *Snapshot, error) {
	goos := req.OS
	if goos == "" {
		goos = runtime.GOOS
	}
	if strings.TrimSpace(req.Cwd) == "" {
		return Data{}, nil, herr.New(ReasonFacts, "launch: the working directory is unknown")
	}
	cwd, err := filepath.Abs(req.Cwd)
	if err != nil {
		return Data{}, nil, herr.Wrap(ReasonFacts, err, "launch: the working directory")
	}
	process := parseEnviron(req.Environ, goos)
	root, err := homedir.Resolve(req.Home, func(name string) (string, bool) {
		e, ok := process[key(name, goos)]
		return e.value, ok
	})
	if err != nil {
		return Data{}, nil, err
	}
	var project, homeFile map[string]entry
	if req.ReadFile != nil {
		if project, err = readEnvFile(req.ReadFile, filepath.Join(cwd, ProjectEnvFile), SourceProject, goos); err != nil {
			return Data{}, nil, err
		}
		configDir := home.New(root).Config()
		homePath := filepath.Join(configDir, HomeEnvFile)
		if homeFile, err = readEnvFile(req.ReadFile, homePath, SourceHome, goos); err != nil {
			return Data{}, nil, err
		}
		// The home file may set HomeOnly names (proxy, endpoints, TLS trust), so it
		// is trusted only when neither it nor config/ is writable by anyone else.
		if homeFile != nil && req.CheckPrivate != nil {
			for _, p := range []string{configDir, homePath} {
				if err := req.CheckPrivate(p); err != nil {
					return Data{}, nil, herr.Wrap(ReasonRead, err,
						"launch: "+homedir.Display(homePath)+" is not trusted; make it and config/ private to their owner")
				}
			}
		}
	}
	env := newSnapshot(goos, process, project, homeFile, systemProxy(req.SystemProxy, goos))
	version := req.Build.Version
	if version == "" {
		version = DevelVersion
	}
	d := Data{
		Profile:  req.Profile,
		Home:     root,
		Cwd:      cwd,
		Version:  version,
		OS:       goos,
		User:     req.User,
		Build:    req.Build,
		Args:     req.Args,
		TTY:      req.TTY,
		Safe:     req.Safe,
		Overlays: req.Overlays,
		Words:    req.Words,
	}
	d.Build.Version = version
	return d.Clone(), env, nil
}

// Probe fills a request from the running process: argv, working directory, OS,
// user, build identity, environment and a file reader. The launcher sets the
// options it parsed (Home, Profile, TTY, Safe, Overlays, Words, SystemProxy).
func Probe() (Request, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return Request{}, herr.Wrap(ReasonFacts, err, "launch: the working directory")
	}
	return Request{
		Args:         slices.Clone(os.Args),
		Cwd:          cwd,
		OS:           runtime.GOOS,
		User:         currentUser(),
		Build:        buildIdentity(),
		Environ:      os.Environ(),
		ReadFile:     os.ReadFile,
		CheckPrivate: home.CheckPrivate,
	}, nil
}

func currentUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	for _, name := range []string{"USER", "USERNAME", "LOGNAME"} {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	return ""
}

func buildIdentity() BuildIdentity {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return BuildIdentity{Version: DevelVersion, GoVersion: runtime.Version()}
	}
	b := BuildIdentity{Version: info.Main.Version, GoVersion: info.GoVersion}
	if b.Version == "" {
		b.Version = DevelVersion
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			b.Revision = s.Value
		case "vcs.time":
			b.Time = s.Value
		case "vcs.modified":
			b.Modified = s.Value == "true"
		}
	}
	return b
}
