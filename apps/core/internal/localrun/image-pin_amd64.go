//go:build linux && cgo && sous_linux_worker

package localrun

// Alpine 3.22.8, Linux amd64 single-platform OCI manifest. Never use a tag
// or the multi-architecture index to resolve a Guest image at runtime.
const imageDigest = "sha256:9db2934886712d877c932e07249c75a59e6ca27adfbccc05261987a17bef2d63"

var imageBlobs = map[string]int{
	"blobs/sha256/" + imageDigest[len("sha256:"):]:                                  1022,
	"blobs/sha256/812e99317af7009fea987b5ad913e63f8494c2d6e596b59b6ac8b8f91f7766e8": 611,
	"blobs/sha256/710f3da1112f360bed5d2908205aeee86b9d04ecd471354cf7049a2f0bdb5c72": 3794375,
}
