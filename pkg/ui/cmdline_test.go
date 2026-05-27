package ui

import (
	"reflect"
	"testing"
)

func TestParseCmdLine(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"  ", nil},
		{"bash", []string{"bash"}},
		{"ssh user@host", []string{"ssh", "user@host"}},
		{"ssh -p 22 user@host", []string{"ssh", "-p", "22", "user@host"}},

		// Shell-required cases.
		{`bash --rcfile <(echo "cd /mnt/repos")`, []string{"bash", "-c", `bash --rcfile <(echo "cd /mnt/repos")`}},
		{"ls | grep foo", []string{"bash", "-c", "ls | grep foo"}},
		{"echo $HOME", []string{"bash", "-c", "echo $HOME"}},
		{"cmd && other", []string{"bash", "-c", "cmd && other"}},
		{"foo > out.txt", []string{"bash", "-c", "foo > out.txt"}},
		{"cd ~/repos", []string{"bash", "-c", "cd ~/repos"}},
		{`echo "hi"`, []string{"bash", "-c", `echo "hi"`}},
	}
	for _, tc := range cases {
		got := ParseCmdLine(tc.in)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ParseCmdLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
