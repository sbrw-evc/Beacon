package response

// SetFFmpeg replaces the lookup of ffmpeg; the returned function restores it.
func SetFFmpeg(f func() (string, error)) func() {
	old := lookFFmpeg
	lookFFmpeg = f
	return func() { lookFFmpeg = old }
}
