package fs

import (
	"strings"
	"testing"
)

func TestStripArchiveExt(t *testing.T) {
	tests := []struct {
		name, base, want string
	}{
		{"tar.gz", "video.tar.gz", "video"},
		{"tar.bz2", "video.tar.bz2", "video"},
		{"tar.xz", "video.tar.xz", "video"},
		{"tgz", "video.tgz", "video"},
		{"tbz2", "video.tbz2", "video"},
		{"single ext", "report.pdf", "report"},
		{"no ext", "LICENSE", "LICENSE"},
		{"dotfile gz", ".hidden.gz", ".hidden"},
		{"multiple dots", "archive.tar.gz", "archive"},
		// stripSuffixFold strips case-insensitively while preserving the rest.
		{"uppercase", "Video.TAR.GZ", "Video"},
		{"mixed case", "Video.Tgz", "Video"},
		{"extension only", ".tar.gz", ""},
		// A lowercase suffix that is not an archive dual extension falls back
		// to the single-extension strip.
		{"non-archive gz", "data.gz", "data"},
		{"tar only", "backup.tar", "backup"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripArchiveExt(tt.base, strings.ToLower(tt.base)); got != tt.want {
				t.Errorf("stripArchiveExt(%q) = %q, want %q", tt.base, got, tt.want)
			}
		})
	}
}

// TestArchiveDestName_Traversal guards the jail escape where an archive named
// "..tar.gz" (or ".tar.gz") derived a destination of ".."/"" and let the
// extraction write outside the jail and quarantine host symlinks.
func TestArchiveDestName_Traversal(t *testing.T) {
	for _, name := range []string{"..tar.gz", ".tar.gz", "..tar", ".tar"} {
		if got, err := archiveDestName("/jail/dir/" + name); err == nil {
			t.Errorf("archiveDestName(%q) = %q, want error", name, got)
		}
	}
	// A normal archive still derives its own directory.
	got, err := archiveDestName("/jail/dir/video.tar.gz")
	if err != nil || got != "video" {
		t.Fatalf("archiveDestName(video.tar.gz) = %q, %v; want video, nil", got, err)
	}
}

func FuzzArchiveDestName(f *testing.F) {
	f.Add("video.tar.gz")
	f.Add("..tar.gz")
	f.Add(".tar.gz")
	f.Fuzz(func(t *testing.T, base string) {
		got, err := archiveDestName("/jail/" + base)
		if err != nil {
			return
		}
		if got == "" || got == "." || got == ".." || strings.ContainsAny(got, "/\\") {
			t.Fatalf("archiveDestName(%q) = %q, must be a single safe path element", base, got)
		}
	})
}

func FuzzStripArchiveExt(f *testing.F) {
	f.Add("video.tar.gz")
	f.Add("Video.TAR.GZ")
	f.Add(".tar.gz")
	f.Add("backup.tar")
	f.Fuzz(func(t *testing.T, base string) {
		got := stripArchiveExt(base, strings.ToLower(base))
		// Stripping only ever removes a suffix, so the result must be a prefix
		// of the input and never grow it.
		if len(got) > len(base) || !strings.HasPrefix(base, got) {
			t.Fatalf("stripArchiveExt(%q) = %q is not a prefix", base, got)
		}
	})
}
