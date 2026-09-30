package localworkspace

import "os"

func openNativePath(root *os.Root, path string) (*os.File, error) {
	return root.Open(path)
}
