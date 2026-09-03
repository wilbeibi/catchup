package session

import (
	"runtime"
	"testing"
)

func TestSameDir(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"/home/u/proj", "/home/u/proj", true},
		{"/home/u/proj/", "/home/u/proj", true},
		{"/home/u/./proj", "/home/u/proj", true},
		{"/home/u/proj", "/home/u/other", false},
		{"", "/home/u/proj", false},
	}
	for _, tt := range tests {
		if got := SameDir(tt.a, tt.b); got != tt.want {
			t.Errorf("SameDir(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

// Case and separator spelling of one Windows directory: agents record
// whichever form their launcher handed them, and on Windows those name the
// same place. On Unix they are two different directories.
func TestSameDirWindowsSpellings(t *testing.T) {
	win := runtime.GOOS == "windows"
	tests := []struct{ a, b string }{
		{`C:\Users\u\proj`, `c:\users\u\proj`},
		{`C:\Users\u\proj`, `C:/Users/u/proj`},
	}
	for _, tt := range tests {
		if got := SameDir(tt.a, tt.b); got != win {
			t.Errorf("SameDir(%q, %q) = %v, want %v on %s", tt.a, tt.b, got, win, runtime.GOOS)
		}
	}
	if SameDir(`C:\Users\u\proj`, `C:\Users\u\other`) {
		t.Error("SameDir matched two different Windows directories")
	}
}
