package station

// image_size.go: refuse a picture the engine cannot make, before the engine
// sees it (workspace docs/표준-결제-적용.md SP-09).
//
// sd-server dies on some sizes: one request for 0x0 ended the container with a
// segfault (exit 139), and every request after it failed until the engine was
// started again. isannd's payment gate already refuses a size off its table
// (GLink pkg/paygate/image.go), but only for a caller it bills. A free node,
// the node's own operators and a request made on this machine all reached the
// engine unchecked. The station is where every one of them meets the engine,
// so the check lives here, on both doors: a job submit and a call handed
// straight to the engine.
//
// The rules are the gate's, kept in step by hand (GLink pkg/paygate/image.go
// imageTenths · archSizes · maxImagesPerJob):
//
//	sizes      512x512 · 512x768 (= 768x512) · 768x768 · 1024x1024 · 1536x1536 · 2048x2048
//	by ARCH    sd15 512x512 · 512x768 · 768x768   sd21 512x768 · 768x768
//	           sdxl · pony · sd3 · flux 1024x1024   (unknown or none: the sizes above)
//	n          1 to 16
//
// A body that leaves the size out passes: a run gets the manifest's 512x512,
// a raw engine body gets the engine's own default.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/isannai/mesh/pkg/engine/manifest"
	"github.com/isannai/mesh/pkg/setup"
)

// pictureSizes are the sizes any picture engine is asked for, "short x long",
// smallest first.
var pictureSizes = []string{"512x512", "512x768", "768x768", "1024x1024", "1536x1536", "2048x2048"}

// pictureArchSizes narrows pictureSizes to what a model architecture draws.
var pictureArchSizes = map[string][]string{
	"sd15": {"512x512", "512x768", "768x768"},
	"sd21": {"512x768", "768x768"},
	"sdxl": {"1024x1024"},
	"pony": {"1024x1024"}, // SDXL-based
	"sd3":  {"1024x1024"},
	"flux": {"1024x1024"},
}

// maxPictures bounds n per request.
const maxPictures = 16

// reasonImageSize is the gate's reason code for the same refusal.
const reasonImageSize = "image_size_unsupported"

var errPictureSize = errors.New("image size not accepted")

// isPictureService reports whether a service's default run makes pictures.
func isPictureService(api *manifest.APISpec) bool {
	return api != nil && api.Run != nil && api.Run.Result.Modality == "image"
}

// engineArch is the ARCH in a service's engine .env (sd15, sdxl, ...), "" when
// unknown. The .env sits beside the engine manifest,
// <root>/artifacts/addon/engines/<engine>/.env, the file isannd's gate reads.
func engineArch(svc setup.ServiceEntry) string {
	if svc.Engine == "" {
		return ""
	}
	dir := filepath.Dir(manifest.AppManifestPath(svc.Engine))
	v, _ := readEnvValue(filepath.Join(dir, ".env"), "ARCH")
	return strings.TrimSpace(v)
}

// checkPictureRequest reads size and count from a picture request body, JSON or
// multipart, and refuses what the engine cannot make. A body it cannot read is
// left to the engine.
func checkPictureRequest(contentType string, body []byte, arch string) error {
	if mt, params, err := mime.ParseMediaType(contentType); err == nil && mt == "multipart/form-data" {
		fields := map[string]any{}
		mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			switch name := part.FormName(); name {
			case "size", "width", "height", "n":
				if part.FileName() == "" {
					v, _ := io.ReadAll(io.LimitReader(part, 64))
					fields[name] = strings.TrimSpace(string(v))
				}
			}
			part.Close()
		}
		return checkPicture(fields, arch)
	}
	var fields map[string]any
	if json.Unmarshal(body, &fields) != nil {
		return nil
	}
	return checkPicture(fields, arch)
}

// checkPicture refuses a size off the table, a size the architecture does not
// draw, and a count outside 1..maxPictures.
func checkPicture(fields map[string]any, arch string) error {
	size, err := pictureSize(fields)
	if err != nil {
		return err
	}
	if size != "" {
		if !contains(pictureSizes, size) {
			return fmt.Errorf("%w: %s is not offered - one of %s", errPictureSize, size, strings.Join(pictureSizes, ", "))
		}
		if sizes, ok := pictureArchSizes[strings.ToLower(arch)]; ok && !contains(sizes, size) {
			return fmt.Errorf("%w: this node runs a %s model, which does not draw %s well - one of %s",
				errPictureSize, arch, size, strings.Join(sizes, ", "))
		}
	}
	if v, present := fields["n"]; present {
		if k, ok := wholeNumber(v); !ok || k < 1 || k > maxPictures {
			return fmt.Errorf("%w: n must be 1 to %d", errPictureSize, maxPictures)
		}
	}
	return nil
}

// pictureSize reads "size" ("WxH") or width + height, as "short x long".
// "" = not given.
func pictureSize(f map[string]any) (string, error) {
	var w, h int
	if s, ok := f["size"].(string); ok && strings.TrimSpace(s) != "" {
		parts := strings.Split(strings.ToLower(strings.TrimSpace(s)), "x")
		if len(parts) != 2 {
			return "", fmt.Errorf("%w: size %q is not WxH", errPictureSize, s)
		}
		var err1, err2 error
		w, err1 = strconv.Atoi(strings.TrimSpace(parts[0]))
		h, err2 = strconv.Atoi(strings.TrimSpace(parts[1]))
		if err1 != nil || err2 != nil {
			return "", fmt.Errorf("%w: size %q is not WxH", errPictureSize, s)
		}
	} else {
		wv, wok := f["width"]
		hv, hok := f["height"]
		if !wok && !hok {
			return "", nil
		}
		var ok1, ok2 bool
		w, ok1 = wholeNumber(wv)
		h, ok2 = wholeNumber(hv)
		if !ok1 || !ok2 {
			return "", fmt.Errorf("%w: width and height must both be whole numbers", errPictureSize)
		}
	}
	if w <= 0 || h <= 0 {
		return "", fmt.Errorf("%w: %dx%d - one of %s", errPictureSize, w, h, strings.Join(pictureSizes, ", "))
	}
	if w > h {
		w, h = h, w
	}
	return fmt.Sprintf("%dx%d", w, h), nil
}

// wholeNumber accepts a JSON number or a numeric string ("${n}" templates and
// form fields arrive as strings), whole values only.
func wholeNumber(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		if x != float64(int(x)) {
			return 0, false
		}
		return int(x), true
	case string:
		k, err := strconv.Atoi(strings.TrimSpace(x))
		return k, err == nil
	}
	return 0, false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// refusePicture answers a refused picture request the way the gate does.
func refusePicture(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error(), Reason: reasonImageSize})
}
