package theme

import (
	"testing"

	"github.com/muesli/termenv"
)

func TestColorProfileWithXtermFallback(t *testing.T) {
	tests := []struct {
		name          string
		detected      termenv.Profile
		stdoutIsTTY   bool
		colorDisabled bool
		ci            string
		termName      string
		want          termenv.Profile
	}{
		{
			name:        "plain interactive xterm",
			detected:    termenv.Ascii,
			stdoutIsTTY: true,
			termName:    "xterm",
			want:        termenv.ANSI,
		},
		{
			name:          "NO_COLOR or CLICOLOR disabled",
			detected:      termenv.Ascii,
			stdoutIsTTY:   true,
			colorDisabled: true,
			termName:      "xterm",
			want:          termenv.Ascii,
		},
		{
			name:        "CI",
			detected:    termenv.Ascii,
			stdoutIsTTY: true,
			ci:          "true",
			termName:    "xterm",
			want:        termenv.Ascii,
		},
		{
			name:        "redirected output",
			detected:    termenv.Ascii,
			stdoutIsTTY: false,
			termName:    "xterm",
			want:        termenv.Ascii,
		},
		{
			name:        "unknown terminal",
			detected:    termenv.Ascii,
			stdoutIsTTY: true,
			termName:    "vt100",
			want:        termenv.Ascii,
		},
		{
			name:        "dumb terminal",
			detected:    termenv.Ascii,
			stdoutIsTTY: true,
			termName:    "dumb",
			want:        termenv.Ascii,
		},
		{
			name:        "existing ANSI256",
			detected:    termenv.ANSI256,
			stdoutIsTTY: true,
			termName:    "xterm",
			want:        termenv.ANSI256,
		},
		{
			name:        "existing TrueColor",
			detected:    termenv.TrueColor,
			stdoutIsTTY: true,
			termName:    "xterm",
			want:        termenv.TrueColor,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := colorProfileWithXtermFallback(
				tc.detected,
				tc.stdoutIsTTY,
				tc.colorDisabled,
				tc.ci,
				tc.termName,
			)
			if got != tc.want {
				t.Errorf("profile = %v, want %v", got, tc.want)
			}
		})
	}
}
