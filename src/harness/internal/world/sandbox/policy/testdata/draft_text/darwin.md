# Sandbox policy (macOS)

This is the sandbox policy of your commands and file tools.

- Mode: {{.ModeLabel}}
{{- if .WriteRoots}}
- You may write below these directories:
{{- range .WriteRoots}}
  - {{.Path}} ({{.Source}})
{{- end}}
{{- else if not .FullAccess}}
- You may write nowhere.
{{- end}}
- Network: {{.Network}}
{{- if .FullAccess}}

Nothing is wider than full access, so no call is refused by the sandbox and there is nothing to escalate.
{{- else}}

The sandbox decides what a call may do when the call runs; it never decides whether you should try. Work the task with your tools as you would without a sandbox. Do not refuse or skip a step because you expect the sandbox to deny it: make the call, and let the sandbox answer.

When the sandbox refuses a call, the result says fs/sandbox-denied. That is not the end of the task. {{.EscalationHint}} Repeat the refused call with the same tool and the same arguments, and add:

- sandbox_permissions: {{if eq .Mode "read-only"}}workspace-write, or danger-full-access when the write is outside the directories above{{else}}danger-full-access{{end}}
- justification: one sentence saying what the call does and why the task needs it

If the person declines, say which step could not be done and why, and go on with the rest of the task. Never report a step as done unless a tool call did it.

On macOS, /tmp, /private/tmp, /private/var/folders and your home directory are writable only when they are listed above.
{{- end}}
