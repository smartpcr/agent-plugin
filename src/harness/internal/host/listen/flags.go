package listen

import (
	"strconv"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// ReasonFlag is a web address flag without a value, with a bad value, or the
// upstream --host (app-cli.md sec. 6.2: the flag is --bind).
const ReasonFlag herr.Reason = "listen/flag"

// AddressFlags reads the web app's address flags, --bind <address> and --port <n>
// (0: the system picks), from its words, as "--flag value" or "--flag=value"; the
// last of each wins. It returns the merge patch of host.listen's config they make,
// nil when neither is given, so the launch binds and a second launch compares the
// same address. Other words are left to their own readers.
func AddressFlags(words []string) (map[string]any, error) {
	patch := map[string]any{}
	for i := 0; i < len(words); i++ {
		w := words[i]
		if w == "--" {
			break
		}
		name, value, inline := strings.Cut(w, "=")
		switch name {
		case "--host":
			return nil, herr.New(ReasonFlag, "dsh web: --host is not a flag; use --bind <address>")
		case "--bind", "--port":
		default:
			continue
		}
		if !inline {
			if i+1 >= len(words) || strings.HasPrefix(words[i+1], "--") {
				return nil, herr.Newf(ReasonFlag, "dsh web: %s needs a value", name)
			}
			i++
			value = words[i]
		}
		if name == "--bind" {
			if strings.TrimSpace(value) == "" {
				return nil, herr.New(ReasonFlag, "dsh web: --bind needs an address")
			}
			patch["bind"] = value
			continue
		}
		port, err := strconv.ParseUint(value, 10, 16)
		if err != nil {
			return nil, herr.Newf(ReasonFlag, "dsh web: --port %q is not a port number (0 to 65535)", value)
		}
		patch["port"] = port
	}
	if len(patch) == 0 {
		return nil, nil
	}
	return patch, nil
}
