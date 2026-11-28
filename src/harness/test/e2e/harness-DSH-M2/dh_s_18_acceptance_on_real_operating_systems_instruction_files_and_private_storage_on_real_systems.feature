@story-harness:DSH-M2 @phase-dh-s-18-acceptance-on-real-operating-systems @stage-instruction-files-and-private-storage-on-real-systems @setup-inline
Feature: Instruction files and private storage on real systems
  implementation-plan Stage 8.8 (impl.md DH-S-18 steps 18.9 and 18.10). The
  instruction-file component (DH-D-CONTEXT-2) follows a real git repository's
  AGENTS.md through a real editor and a real branch switch into the next model
  request, and refuses a link that leaves the repository; the harness home and
  what the attachment and spill stores keep in it are closed to every other OS
  account. The release build does not mount either component yet (DH-S-18.md
  "Stage 8.8 blockers"), so these scenarios drive the components the binary will
  mount, on this machine, and check that the evidence record claims no lane run
  it does not have.

  # TC-07s, TC-21s4 [proof: service:git-and-editor; deps: git and an editor (sh / powershell) on PATH, os-process]
  Scenario: DH-E-CONTEXT-2 -- an edited, switched AGENTS.md reaches the next model request and an outside link is refused
    Given git and an editor script are installed on this machine
    And a real git repository on branch "main" whose "AGENTS.md" holds the section "## Build"
    And the branch "release" of the repository adds the section "## Release" to "AGENTS.md"
    And the repository's "sub/AGENTS.md" is a link to an instruction file outside the repository
    And the instructions contributor follows a session working in "sub" of the repository
    When the first model request is assembled
    Then the request carries a "baseline" instructions message containing "## Build"
    And the instructions message does not contain the outside file's text
    And the instructions message refuses "sub/AGENTS.md" with "fs/sandbox-denied"
    When the editor script adds the section "## Style" to "AGENTS.md"
    And the instructions contributor notices the change within 10 seconds
    And the next model request is assembled
    Then the request carries an "update" instructions message containing "## Style"
    And the instructions message contains "## Build"
    And the instructions message does not contain the outside file's text
    And the instructions message refuses "sub/AGENTS.md" with "fs/sandbox-denied"
    When the edit is committed and the branch "release" is checked out
    And the instructions contributor notices the change within 10 seconds
    And the next model request is assembled
    Then the request carries an "update" instructions message containing "## Release"
    And the instructions message does not contain "## Style"
    And the instructions message does not contain the outside file's text
    And the instructions message refuses "sub/AGENTS.md" with "fs/sandbox-denied"
    When the next model request is assembled
    Then the request carries no instructions message
    And the DH-S-18 evidence claims no "DH-E-CONTEXT-2" lane row it has not run

  # TC-21s3 [proof: service:os-process; deps: two-account machine per runbook (M2-EN-5), filesystem]
  # The second account is a real, ordinary local account other than the runner's:
  # the runbook's (DSH_SCENARIO_STORAGE6_USER, _PUBLIC, _LIST, _READ, as the s18
  # two-account jobs of harness-scenarios.yml set them), or, where the run is root
  # or has passwordless sudo, one the scenario makes with useradd and deletes. On
  # Windows without the runbook's account, the test runs this scenario in the
  # machine's WSL as root instead (subtest linux-wsl). Every list and read below is
  # a real process run as that account, and a failure counts as denied only when
  # the OS's refusal (EACCES / ERROR_ACCESS_DENIED) is what it printed. It never
  # skips: a machine with no second account fails it, naming the missing inputs.
  @second-account
  Scenario: DH-E-STORAGE-6 -- a second OS account is denied every read of the first user's harness home
    Given a second ordinary OS account on this machine
    And a harness home created private by the harness in a directory both accounts can traverse
    And the attachment store keeps one attachment in the home
    And the spill store keeps one spill file in the home
    And the second account can list the shared directory and read a file opened to everyone
    When the second account lists every directory and reads every file of the home
    Then the operating system denies every one of those reads
    And no denied read printed a stored name or stored bytes
    And the home's owner can still read the attachment and the spill file
    And the DH-S-18 evidence claims no "DH-E-STORAGE-6" lane row it has not run

  # TC-21s3 pre-check, not the DH-E-STORAGE-6 acceptance [proof: service:os-process; deps: filesystem]
  # Runs on every machine. Windows: real opens under a restricted copy of the
  # owner's token (owner, SYSTEM and Administrators deny-only). POSIX: real reads
  # as uid 65534 when root, else the owner and mode bits the kernel checks.
  Scenario: TC-21s3 pre-check -- no principal but the owner is granted a read of the home, and an opened spill/ is refused
    Given a harness home created private by the harness in a directory both accounts can traverse
    And the attachment store keeps one attachment in the home
    And the spill store keeps one spill file in the home
    When a principal that is not the home's owner is checked against every directory and file of the home
    Then the operating system denies every one of those reads
    And the same principal can read a control file the test opens to everyone
    When "spill" is opened to other accounts
    Then the spill store refuses to start with "home/not-private" naming "spill"
    And the harness refuses the home with "home/not-private" naming "spill"

