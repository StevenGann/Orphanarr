// Package web embeds the user interface.
//
// The interface is a single self-contained HTML file with no build step.
// DESIGN ratified a Preact SPA (D2) and recorded, in the same row, that
// "the seam is the API: if this flips, nothing else in the design changes".
// This flips it, deliberately, for v0.1:
//
//   - no Node toolchain in CI, no lockfile with hundreds of transitive
//     dependencies, no npm audit treadmill for six screens;
//   - the binary is genuinely self-contained, which is the property the
//     single-static-binary decision exists to deliver;
//   - the API contract is unchanged, so replacing this with the ratified
//     SPA later is additive.
//
// The losing position is recorded here rather than in a commit message,
// because that is where the next person will look.
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed assets
var assets embed.FS

// Handler serves the UI at /.
func Handler() http.Handler {
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		// Unreachable: the embed is compile-time. Panicking beats serving
		// a blank page whose cause is invisible.
		panic("web: embedded assets missing: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A strict CSP because this program is remote arbitrary file write
		// by design (BRIEF Q19) and the UI takes destination paths as
		// input. 'unsafe-inline' is present only for the single inlined
		// script and style; nothing is fetched from anywhere.
		w.Header().Set("Content-Security-Policy",
			"default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; "+
				"connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		fileServer.ServeHTTP(w, r)
	})
}
