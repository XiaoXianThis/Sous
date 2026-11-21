//go:build cgo && ((darwin && arm64) || (linux && (amd64 || arm64) && sous_linux_worker))

package localrun

// Alpine 3.22.8, Linux arm64 single-platform OCI manifest.
const imageDigest = "sha256:e233f2058b0628266762f745a2c17e623b418e0090f47d223909d1f9a7d4367e"

// All consumed blobs are pinned. The archive's names, index and metadata are
// never passed to the runtime. In particular, an archive cannot override
// unrelated cached images or exercise tar extraction with its paths/links.
var imageBlobs = map[string]int{
	"blobs/sha256/" + imageDigest[len("sha256:"):]:                                  1025,
	"blobs/sha256/062964a69725bb96611dae2ac911a5a1ab3f8d32fe6f096f267d77f09e55a99c": 627,
	"blobs/sha256/fea5b91588ca539ff8a89d7d5df6c6d65d6ab7bd89145ba57f32c302cf02ffbe": 4127385,
}
