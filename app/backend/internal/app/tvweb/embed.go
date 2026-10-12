// Package tvweb holds the TV wallboard page: plain HTML, CSS and JavaScript without a build
// step, so it runs on smart-TV and kiosk browsers and is served even without the web UI.
package tvweb

import "embed"

//go:embed index.html tv.js tv.css
var FS embed.FS
