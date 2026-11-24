package control

import "net/http"

// The product name and its forms. The admin pages show productName; the boot
// menu is plain ASCII for iPXE and shows the wordmark as text.
const (
	productName        = "oh my boot_"
	productMenuTitle   = "oh my boot_"
	defaultMenuMessage = "oh my boot_ network boot is ready"
)

// brandFiles are the browser icons, served without a session so the sign-in
// page has them too.
var brandFiles = map[string]string{
	"/favicon.ico":                 "image/x-icon",
	"/assets/favicon.svg":          "image/svg+xml",
	"/assets/apple-touch-icon.png": "image/png",
}

func brandFile(w http.ResponseWriter, r *http.Request) {
	contentType, ok := brandFiles[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	name := r.URL.Path[len("/"):]
	if i := len("assets/"); len(name) > i && name[:i] == "assets/" {
		name = name[i:]
	}
	data, err := templateFS.ReadFile("templates/brand/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(data)
}
