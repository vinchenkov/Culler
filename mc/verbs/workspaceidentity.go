package verbs

import (
	"fmt"
	"os"
	"syscall"
)

func workspaceDirectoryIdentity(info os.FileInfo, exists bool) string {
	stat := info.Sys().(*syscall.Stat_t)
	return visionRevision([]byte(fmt.Sprintf("%t:%d:%d", exists, stat.Dev, stat.Ino)))
}
