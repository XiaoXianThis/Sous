// package builds the desktop app with the sous daemon inside:
//
//	macOS:   <out>/sous.app (Contents/MacOS/{sous-desktop,sous}), signed with
//	         the local "Sous Dev Signing" identity when present, and a zip
//	Windows: <out>/sous-desktop_<version>_windows_<arch>.zip with both .exe
//
// Usage (in apps/desktop):
//
//	go run ./tools/package -goos darwin -sous ../../build/core/sous -out ../../build/desktop -version v0.3.0
package main

import (
	"archive/zip"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func main() {
	goos := flag.String("goos", runtime.GOOS, "darwin or windows")
	goarch := flag.String("goarch", runtime.GOARCH, "target architecture")
	sous := flag.String("sous", "", "the sous binary for the same target")
	out := flag.String("out", "", "output directory")
	ver := flag.String("version", "dev", "version")
	page := flag.String("page", os.Getenv("SOUS_PAGE_URL"), "default page URL")
	flag.Parse()
	if *sous == "" || *out == "" {
		log.Fatal("need -sous and -out")
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	ldflags := fmt.Sprintf("-s -w -X main.version=%s -X main.pageURL=%s", *ver, *page)
	switch *goos {
	case "darwin":
		mac(*out, *sous, *goarch, *ver, ldflags)
	case "windows":
		windows(*out, *sous, *goarch, *ver, ldflags)
	default:
		log.Fatalf("unsupported -goos %q", *goos)
	}
}

func build(goos, goarch, out, ldflags string) {
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", out, ".")
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch)
	if goos == "darwin" {
		cmd.Env = append(cmd.Env, "CGO_ENABLED=1", "MACOSX_DEPLOYMENT_TARGET=11.0")
	} else {
		cmd.Env = append(cmd.Env, "CGO_ENABLED=0")
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatal("go build: ", err)
	}
}

func mac(out, sous, goarch, ver, ldflags string) {
	app := filepath.Join(out, "sous.app")
	os.RemoveAll(app)
	macos := filepath.Join(app, "Contents", "MacOS")
	res := filepath.Join(app, "Contents", "Resources")
	must(os.MkdirAll(macos, 0o755))
	must(os.MkdirAll(res, 0o755))
	build("darwin", goarch, filepath.Join(macos, "sous-desktop"), ldflags)
	copyFile(sous, filepath.Join(macos, "sous"), 0o755)
	icns(filepath.Join(res, "icon.icns"))
	plist := strings.NewReplacer("{{VERSION}}", strings.TrimPrefix(ver, "v")).Replace(infoPlist)
	must(os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644))
	sign(app)
	zipName := filepath.Join(out, fmt.Sprintf("sous-desktop_%s_darwin_%s.zip", strings.TrimPrefix(ver, "v"), goarch))
	os.Remove(zipName)
	run("ditto", "-c", "-k", "--keepParent", app, zipName)
	fmt.Println(app)
	fmt.Println(zipName)
}

// icns turns assets/icon.png into the bundle icon (macOS tools only).
func icns(dst string) {
	set := filepath.Join(filepath.Dir(dst), "icon.iconset")
	must(os.MkdirAll(set, 0o755))
	defer os.RemoveAll(set)
	for _, n := range []int{16, 32, 64, 128, 256} {
		run("sips", "-z", fmt.Sprint(n), fmt.Sprint(n), "assets/icon.png", "--out", filepath.Join(set, fmt.Sprintf("icon_%dx%d.png", n, n)))
	}
	run("iconutil", "-c", "icns", set, "-o", dst)
}

// sign signs the bundle with the same self-signed identity core uses
// (decision 0026). The sous binary inside is copied already signed by core's
// build, so its identifier and macOS privacy permissions stay the same.
// Without the identity the bundle is ad-hoc signed.
func sign(app string) {
	home, _ := os.UserHomeDir()
	kc := filepath.Join(home, "Library", "Keychains", "sous-signing.keychain-db")
	hash := ""
	if out, err := exec.Command("security", "find-identity", "-p", "codesigning", kc).Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if f := strings.Fields(line); len(f) > 2 && strings.Contains(line, `"Sous Dev Signing"`) {
				hash = f[1]
				break
			}
		}
	}
	if hash == "" {
		log.Print("没有找到 Sous Dev Signing 身份，改用 ad-hoc 签名（每次更新后 macOS 的隐私授权可能要重新给）")
		run("codesign", "--force", "-s", "-", "-i", "cc.wxian.sous.desktop", app)
		return
	}
	// codesign finds an identity only in a keychain on the search list.
	raw, err := exec.Command("security", "list-keychains", "-d", "user").Output()
	must(err)
	var list []string
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		list = append(list, strings.Trim(strings.TrimSpace(l), `"`))
	}
	added := !contains(list, kc)
	if added {
		run("security", append([]string{"list-keychains", "-d", "user", "-s"}, append(list, kc)...)...)
		defer run("security", append([]string{"list-keychains", "-d", "user", "-s"}, list...)...)
	}
	run("codesign", "--force", "--sign", hash, "--keychain", kc, "--identifier", "cc.wxian.sous.desktop", "--timestamp=none", app)
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func windows(out, sous, goarch, ver, ldflags string) {
	dir, err := os.MkdirTemp("", "sous-desktop-win")
	must(err)
	defer os.RemoveAll(dir)
	exe := filepath.Join(dir, "sous-desktop.exe")
	build("windows", goarch, exe, ldflags+" -H windowsgui")
	zipName := filepath.Join(out, fmt.Sprintf("sous-desktop_%s_windows_%s.zip", strings.TrimPrefix(ver, "v"), goarch))
	f, err := os.Create(zipName)
	must(err)
	zw := zip.NewWriter(f)
	for name, src := range map[string]string{"sous/sous-desktop.exe": exe, "sous/sous.exe": sous} {
		w, err := zw.Create(name)
		must(err)
		r, err := os.Open(src)
		must(err)
		_, err = io.Copy(w, r)
		r.Close()
		must(err)
	}
	must(zw.Close())
	must(f.Close())
	fmt.Println(zipName)
}

func copyFile(src, dst string, mode os.FileMode) {
	raw, err := os.ReadFile(src)
	must(err)
	must(os.WriteFile(dst, raw, mode))
}

func run(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = io.Discard, os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("%s: %v", name, err)
	}
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

const infoPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key><string>sous</string>
	<key>CFBundleDisplayName</key><string>sous</string>
	<key>CFBundleIdentifier</key><string>cc.wxian.sous.desktop</string>
	<key>CFBundleExecutable</key><string>sous-desktop</string>
	<key>CFBundleIconFile</key><string>icon</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>{{VERSION}}</string>
	<key>CFBundleVersion</key><string>{{VERSION}}</string>
	<key>LSMinimumSystemVersion</key><string>11.0</string>
	<key>LSUIElement</key><true/>
	<key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
`
