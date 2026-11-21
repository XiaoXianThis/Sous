//go:build cgo && ((darwin && arm64) || (linux && (amd64 || arm64) && sous_linux_worker))

package localrun

import (
	"archive/tar"
	"bytes"
	"testing"
)

func TestVerifyImageArchiveRejectsMissingUnverifiedOrSpecialBlobs(t *testing.T) {
	expected := "blobs/sha256/" + imageDigest[len("sha256:"):]
	for _, h := range []tar.Header{
		{Name: "unrelated-file", Typeflag: tar.TypeReg, Size: 7},
		{Name: expected, Typeflag: tar.TypeReg, Size: int64(imageBlobs[expected])},
		{Name: expected, Typeflag: tar.TypeSymlink, Linkname: "./other"},
	} {
		var b bytes.Buffer
		tw := tar.NewWriter(&b)
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(make([]byte, h.Size)); err != nil {
			t.Fatal(err)
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := verifyImageArchive(&b); err == nil {
			t.Fatalf("accepted an unverified OCI archive with %q", h.Name)
		}
	}
}
