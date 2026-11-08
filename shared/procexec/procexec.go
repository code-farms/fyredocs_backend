// Package procexec builds *exec.Cmd values for the external document tools the
// workers shell out to (LibreOffice, Ghostscript, Poppler, Tesseract, Python
// helpers) so that cancelling a job actually stops the work.
//
// The obvious exec.CommandContext is not enough for these tools: on context
// cancellation it kills only the direct child. `libreoffice` is a launcher that
// forks soffice.bin, pdf2docx is a Python process that loads MuPDF, and
// unoconvert is a Python client — their descendants survive the kill, keep
// burning CPU and holding tmpfs files after the job has failed, and keep the
// inherited stdout/stderr pipes open so CombinedOutput blocks until they finish.
// Command fixes both: every command runs in its own process group that is
// killed as a whole on cancellation, and WaitDelay bounds how long Wait waits
// for those pipes afterwards.
//
// This is infrastructure, not business logic: it knows nothing about any tool,
// which is what makes it eligible for shared/ under CLAUDE.md §2. Timeouts stay
// with the caller's context; resource limits and an environment allow-list are
// planned additions (docs/developer/architecture/next-generation-document-engine/
// 13-language-and-technology-selection.md §5.3).
package procexec

import (
	"context"
	"os/exec"
	"time"
)

// DefaultWaitDelay is how long Wait keeps reading a cancelled command's output
// pipes before closing them. Long enough for a killed process group to exit and
// flush; short enough that a descendant which somehow escaped the group cannot
// hold a job open. 5 s per doc 13 §5.3.
const DefaultWaitDelay = 5 * time.Second

// Command returns an *exec.Cmd for name/args bound to ctx, like
// exec.CommandContext, with two differences that only matter on cancellation:
//
//   - On platforms with process groups the command becomes the leader of a new
//     group, and ctx cancellation kills the whole group (SIGKILL), not just the
//     direct child.
//   - WaitDelay is DefaultWaitDelay, so Run/Output/CombinedOutput return shortly
//     after cancellation even if something still holds the output pipes.
//
// Normal (non-cancelled) runs behave exactly as with exec.CommandContext. The
// caller may still set Dir, Env, Stdin, Stdout and Stderr; it must not replace
// SysProcAttr or Cancel, or group killing is lost.
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = DefaultWaitDelay
	setProcessGroup(cmd)
	return cmd
}
