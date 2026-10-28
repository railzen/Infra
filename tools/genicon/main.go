// genicon renders assets/icon.svg into a multi-resolution .ico and writes the
// Windows resource object (icon + manifest + version info) that `go build`
// links into Intra.exe.
//
//	cd tools/genicon && go run . -root ../..
package main

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"path/filepath"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
	xdraw "golang.org/x/image/draw"
)

const manifest = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <assemblyIdentity type="win32" name="Intra" version="1.0.0.0" processorArchitecture="*"/>
  <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1">
    <application>
      <supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/>
    </application>
  </compatibility>
  <application xmlns="urn:schemas-microsoft-com:asm.v3">
    <windowsSettings>
      <dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true/pm</dpiAware>
      <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">PerMonitorV2</dpiAwareness>
      <longPathAware xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">true</longPathAware>
    </windowsSettings>
  </application>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security><requestedPrivileges><requestedExecutionLevel level="asInvoker" uiAccess="false"/></requestedPrivileges></security>
  </trustInfo>
</assembly>`

func render(svg []byte, size int) image.Image {
	icon, err := oksvg.ReadIconStream(bytes.NewReader(svg))
	if err != nil {
		log.Fatal(err)
	}
	icon.SetTarget(0, 0, float64(size), float64(size))
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	scanner := rasterx.NewScannerGV(size, size, img, img.Bounds())
	icon.Draw(rasterx.NewDasher(size, size, scanner), 1)
	return img
}

func main() {
	root := flag.String("root", "../..", "repository root")
	ver := flag.String("version", "1.0.0", "file version")
	flag.Parse()

	svg, err := os.ReadFile(filepath.Join(*root, "assets", "icon.svg"))
	if err != nil {
		log.Fatal(err)
	}
	// rasterx does not scale stroke widths with the target, so render once at
	// full size and downsample.
	big := render(svg, 256)
	sizes := []int{16, 20, 24, 32, 40, 48, 64, 96, 128, 256}
	var imgs []image.Image
	for _, s := range sizes {
		if s == 256 {
			imgs = append(imgs, big)
			continue
		}
		dst := image.NewRGBA(image.Rect(0, 0, s, s))
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), big, big.Bounds(), xdraw.Src, nil)
		imgs = append(imgs, dst)
	}
	ico, err := winres.NewIconFromImages(imgs)
	if err != nil {
		log.Fatal(err)
	}

	// a standalone .ico and preview png, handy for shortcuts and docs
	f, err := os.Create(filepath.Join(*root, "assets", "intra.ico"))
	if err != nil {
		log.Fatal(err)
	}
	ico.SaveICO(f)
	f.Close()
	pf, _ := os.Create(filepath.Join(*root, "assets", "intra-256.png"))
	png.Encode(pf, imgs[len(imgs)-1])
	pf.Close()

	rs := winres.ResourceSet{}
	rs.SetIcon(winres.Name("APP"), ico)
	rs.Set(winres.RT_MANIFEST, winres.ID(1), 0, []byte(manifest))

	vi := version.Info{}
	vi.SetFileVersion(*ver)
	vi.SetProductVersion(*ver)
	vi.Set(0, version.ProductName, "Intra")
	vi.Set(0, version.FileDescription, "Intra")
	vi.Set(0, version.OriginalFilename, "Intra.exe")
	vi.Set(0, version.LegalCopyright, "Intra")
	rs.SetVersionInfo(vi)

	out, err := os.Create(filepath.Join(*root, "rsrc_windows_amd64.syso"))
	if err != nil {
		log.Fatal(err)
	}
	defer out.Close()
	if err := rs.WriteObject(out, winres.ArchAMD64); err != nil {
		log.Fatal(err)
	}
	fmt.Println("wrote rsrc_windows_amd64.syso and assets/intra.ico")
}
