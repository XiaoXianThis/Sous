package localrun

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func reviewFixture(t *testing.T, input map[string]string, change map[string]*string) (string, string, Review) {
	t.Helper()
	root := fixture(t)
	project := filepath.Join(root, "project")
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatal(err)
	}
	for p, v := range input {
		full := filepath.Join(project, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		put(t, full, v)
	}
	before, err := capture(context.Background(), project, nil)
	if err != nil {
		t.Fatal(err)
	}
	after := newSnapshot()
	for p, v := range before.files {
		after.files[p] = v
	}
	for p, v := range change {
		if v == nil {
			delete(after.files, p)
		} else {
			after.files[p] = []byte(*v)
		}
	}
	result, err := writeReport(root, "run", before, after, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := InspectReview(result)
	if err != nil || !r.CanApply {
		t.Fatalf("review not applicable: %+v %v", r, err)
	}
	return project, result, r
}

func readText(t *testing.T, file string) string {
	t.Helper()
	v, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(v)
}

func TestApplyReviewedFiles(t *testing.T) {
	ctx := context.Background()
	updated, added, addedAgain := "改过的内容\n", "new file", "next file"
	project, result, review := reviewFixture(t, map[string]string{"src/original.txt": "old version\n", "src/obsolete.txt": "keep its backup", "unchanged.txt": "normal"}, map[string]*string{"src/original.txt": &updated, "src/obsolete.txt": nil, "new/sub/a.txt": &added, "new/sub/b.txt": &addedAgain})
	orig := filepath.Join(project, "src/original.txt")
	if err := os.Chmod(orig, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := unix.Setxattr(orig, "user.sous-test", []byte("retain local metadata"), 0); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"src/original.txt", "src/obsolete.txt", "new/sub/a.txt", "new/sub/b.txt"} {
		applied, err := ApplyReview(ctx, result, review.Revision, path)
		if err != nil || applied.Path != path {
			t.Fatalf("apply %s: %+v %v", path, applied, err)
		}
		switch path {
		case "src/original.txt":
			if readText(t, orig) != updated || readText(t, filepath.Join(project, applied.Backup)) != "old version\n" {
				t.Fatal("modified file or backup is wrong")
			}
			if st, err := os.Stat(orig); err != nil || st.Mode().Perm() != 0o640 {
				t.Fatalf("file permissions lost: %v %v", st, err)
			}
			var attr [100]byte
			n, err := unix.Getxattr(orig, "user.sous-test", attr[:])
			if err != nil || string(attr[:n]) != "retain local metadata" {
				t.Fatalf("metadata lost: %v", err)
			}
		case "src/obsolete.txt":
			if _, err := os.Lstat(filepath.Join(project, path)); !os.IsNotExist(err) {
				t.Fatalf("not removed: %v", err)
			}
			if readText(t, filepath.Join(project, applied.Backup)) != "keep its backup" {
				t.Fatal("lost original content")
			}
		default:
			if v := readText(t, filepath.Join(project, path)); v != added && v != addedAgain {
				t.Fatalf("unexpected new file contents: %q", v)
			}
		}
		if _, err := ApplyReview(ctx, result, review.Revision, path); err == nil {
			t.Fatalf("replayed the same change: %s", path)
		}
	}
	if readText(t, filepath.Join(project, "unchanged.txt")) != "normal" {
		t.Fatal("unselected file changed")
	}
}

func TestApplyRefusesBeforeMutating(t *testing.T) {
	ctx := context.Background()
	proposal := "agent change"
	for _, label := range []string{"updated-before-apply", "concurrent-edit", "replaced-parent", "symlink-target", "new-file-conflict", "tampered-artifact", "changed-revision"} {
		t.Run(label, func(t *testing.T) {
			project, result, r := reviewFixture(t, map[string]string{"src/note.txt": "baseline"}, map[string]*string{"src/note.txt": &proposal, "new/notes/added.txt": &proposal})
			file := filepath.Join(project, "src/note.txt")
			path, version := "src/note.txt", r.Revision
			var race func()
			switch label {
			case "updated-before-apply":
				put(t, file, "user update")
			case "concurrent-edit":
				race = func() { put(t, file, "concurrent edit") }
			case "replaced-parent":
				if err := os.Rename(filepath.Dir(file), filepath.Join(project, "previous")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Dir(file), 0o700); err != nil {
					t.Fatal(err)
				}
				put(t, file, "different directory")
			case "symlink-target":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				outside := filepath.Join(filepath.Dir(project), "outside.txt")
				put(t, outside, "outside")
				if err := os.Symlink(outside, file); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if readText(t, outside) != "outside" {
						t.Fatal("escaped target boundary")
					}
				}()
			case "new-file-conflict":
				path = "new/notes/added.txt"
				race = func() {
					full := filepath.Join(project, path)
					if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
						t.Fatal(err)
					}
					put(t, full, "created locally")
				}
			case "tampered-artifact":
				for _, c := range r.Changes {
					if c.Path == path {
						put(t, filepath.Join(result, c.AfterFile), "different payload")
					}
				}
			case "changed-revision":
				version = strings.Repeat("1", 64)
			}
			if applied, err := applyReview(ctx, result, version, path, race); err == nil || applied.Path != "" {
				t.Fatalf("apply should have refused: %+v %v", applied, err)
			}
			if readText(t, file) == proposal {
				t.Fatal("unapproved proposal reached original file")
			}
			if label == "new-file-conflict" && readText(t, filepath.Join(project, path)) != "created locally" {
				t.Fatal("overwrote new local file")
			}
			if hits, _ := filepath.Glob(filepath.Join(filepath.Dir(file), ".sous-writeback-*")); len(hits) != 0 {
				t.Fatalf("staging file survived refusal: %v", hits)
			}
			if hits, _ := filepath.Glob(filepath.Join(project, ".sous-writeback-*")); len(hits) != 0 {
				t.Fatalf("staging directory survived refusal: %v", hits)
			}
		})
	}
}

func TestSingleFileGrantCannotApplySibling(t *testing.T) {
	d := fixture(t)
	source := filepath.Join(d, "note.txt")
	put(t, source, "original")
	before, err := capture(context.Background(), source, nil)
	if err != nil {
		t.Fatal(err)
	}
	after := newSnapshot()
	after.files["note.txt"] = []byte("changed")
	after.files["sibling.txt"] = []byte("not authorized")
	where, err := writeReport(d, "run", before, after, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	review, err := InspectReview(where)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyReview(context.Background(), where, review.Revision, "sibling.txt"); err == nil {
		t.Fatal("single-file grant widened to directory")
	}
	if _, err := os.Stat(filepath.Join(d, "sibling.txt")); !os.IsNotExist(err) {
		t.Fatal("wrote sibling file")
	}
	if got, err := ApplyReview(context.Background(), where, review.Revision, "note.txt"); err != nil || got.Backup == "" {
		t.Fatalf("could not apply authorized file: %+v %v", got, err)
	}
	if readText(t, source) != "changed" {
		t.Fatal("single-file apply not persisted")
	}
}

func TestPreviewVerifiesAndBoundsUntrustedContent(t *testing.T) {
	changed := strings.Repeat("X", 64<<10) + "will be truncated"
	control := "hello\x1b\u202e world"
	binary := "new\x00binary"
	_, result, review := reviewFixture(t, map[string]string{"large.txt": "baseline", "ctrl.txt": "plain"}, map[string]*string{"large.txt": &changed, "ctrl.txt": &control, "bytes.txt": &binary})
	preview, err := PreviewFile(result, review.Revision, "large.txt")
	if err != nil || !preview.Truncated || preview.BeforeText == nil || *preview.BeforeText != "baseline" || preview.AfterText == nil || len(*preview.AfterText) != 64<<10 {
		t.Fatal("text preview is not correctly bounded", err)
	}
	preview, err = PreviewFile(result, review.Revision, "ctrl.txt")
	if err != nil || preview.AfterText == nil || *preview.AfterText != "hello�� world" {
		t.Fatal("untrusted control characters survived", err)
	}
	preview, err = PreviewFile(result, review.Revision, "bytes.txt")
	if err != nil || !preview.Binary || preview.AfterText != nil {
		t.Fatal("binary data rendered as text", err)
	}
	for _, change := range review.Changes {
		if change.Path == "ctrl.txt" {
			put(t, filepath.Join(result, change.BeforeFile), "tampered")
		}
	}
	if _, err := PreviewFile(result, review.Revision, "ctrl.txt"); err == nil {
		t.Fatal("previewed tampered artifact")
	}
}
