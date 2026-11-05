package deployment

import (
	"archive/tar"
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func projectArchive(t *testing.T, headers ...tar.Header) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, header := range headers {
		if err := writer.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestProjectArchiveRejectsPrivilegeOutsideStandardDirectories(t *testing.T) {
	for _, name := range []string{"tmp/hidden", "var/lib/tool", "home/agent/.local/bin/tool"} {
		for _, mode := range []int64{04755, 02755} {
			_, err := readProjectImage(bytes.NewReader(projectArchive(t, tar.Header{Name: name, Mode: mode, Typeflag: tar.TypeReg})))
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("privileged archive file accepted: %s %o (%v)", name, mode, err)
			}
		}
	}
	_, err := readProjectImage(bytes.NewReader(projectArchive(t, tar.Header{Name: "opt/tool", Mode: 0755, Typeflag: tar.TypeReg, Xattrs: map[string]string{"security.capability": "capability"}})))
	if err == nil || !strings.Contains(err.Error(), "capabilities") {
		t.Fatalf("file capabilities accepted: %v", err)
	}
}

func TestProjectArchiveRejectsInvalidPathsAndDevices(t *testing.T) {
	for _, header := range []tar.Header{
		{Name: "../outside", Mode: 0644, Typeflag: tar.TypeReg},
		{Name: "/absolute", Mode: 0644, Typeflag: tar.TypeReg},
		{Name: "opt/../outside", Mode: 0644, Typeflag: tar.TypeReg},
		{Name: "dev/escalation", Mode: 0666, Typeflag: tar.TypeChar, Devmajor: 1, Devminor: 3},
	} {
		if _, err := readProjectImage(bytes.NewReader(projectArchive(t, header))); err == nil {
			t.Fatalf("invalid archive entry accepted: %+v", header)
		}
	}
}

func testLibraryCache(entries [][2]string) []byte {
	body := make([]byte, 48+24*len(entries))
	copy(body, "glibc-ld.so.cache1.1")
	binary.LittleEndian.PutUint32(body[20:24], uint32(len(entries)))
	for i, entry := range entries {
		offset := 48 + i*24
		binary.LittleEndian.PutUint32(body[offset:offset+4], 0x303)
		binary.LittleEndian.PutUint32(body[offset+4:offset+8], uint32(len(body)))
		body = append(body, append([]byte(entry[0]), 0)...)
		binary.LittleEndian.PutUint32(body[offset+8:offset+12], uint32(len(body)))
		body = append(body, append([]byte(entry[1]), 0)...)
	}
	return body
}

func projectPlatformFixture() map[string]projectImageFile {
	files := map[string]projectImageFile{}
	for _, name := range []string{".", "etc", "usr", "usr/local", "home", "opt", "mc", "mc/spine", "mc/private", "mc/private/attachments", "mc/private/attachments/out", "mc/attachments", "mc/attachments/in", "mc/codex", "home/mc-model", "workspace", "cache", "usr/lib", "usr/lib/aarch64-linux-gnu", "etc/ld.so.conf.d"} {
		files[name] = projectImageFile{Kind: tar.TypeDir, Mode: 0755}
	}
	files["lib"] = projectImageFile{Kind: tar.TypeSymlink, Mode: 0777, Link: "usr/lib"}
	files["usr/lib/aarch64-linux-gnu/libc.so.6"] = projectImageFile{Kind: tar.TypeReg, Mode: 0755, Digest: "base-library"}
	files["etc/ld.so.conf"] = projectImageFile{Kind: tar.TypeReg, Mode: 0644, Digest: "base-loader-config"}
	files["etc/nsswitch.conf"] = projectImageFile{Kind: tar.TypeReg, Mode: 0644, Digest: "base-nss-config"}
	files["etc/passwd"] = projectImageFile{Kind: tar.TypeReg, Mode: 0644, Body: []byte("root:x:0:0:root:/root:/bin/bash\nmc-completion:x:10001:10001::/home/mc-completion:/usr/sbin/nologin\nmc-model:x:10002:10002::/home/mc-model:/usr/sbin/nologin\n")}
	files["etc/group"] = projectImageFile{Kind: tar.TypeReg, Mode: 0644, Body: []byte("root:x:0:\nmc-completion:x:10001:\nmc-model:x:10002:\n")}
	files["etc/ld.so.cache"] = projectImageFile{Kind: tar.TypeReg, Mode: 0644, Body: testLibraryCache([][2]string{{"libc.so.6", "/lib/aarch64-linux-gnu/libc.so.6"}})}
	return files
}

func TestProjectImageAcceptsAdditionalDevelopmentLibrary(t *testing.T) {
	base, candidate := projectPlatformFixture(), projectPlatformFixture()
	candidate["usr/lib/aarch64-linux-gnu/libpq.so.5"] = projectImageFile{Kind: tar.TypeReg, Mode: 0644, Digest: "postgres-library"}
	file := candidate["etc/ld.so.cache"]
	file.Body = testLibraryCache([][2]string{{"libpq.so.5", "/lib/aarch64-linux-gnu/libpq.so.5"}, {"libc.so.6", "/lib/aarch64-linux-gnu/libc.so.6"}})
	candidate["etc/ld.so.cache"] = file
	if err := compareProjectPlatform(base, candidate); err != nil {
		t.Fatal(err)
	}
}

func TestProjectImageRejectsPlatformReplacement(t *testing.T) {
	cases := map[string]func(map[string]projectImageFile){
		"library": func(files map[string]projectImageFile) {
			file := files["usr/lib/aarch64-linux-gnu/libc.so.6"]
			file.Digest = "replacement-library"
			files["usr/lib/aarch64-linux-gnu/libc.so.6"] = file
		},
		"root access": func(files map[string]projectImageFile) {
			file := files["."]
			file.Mode = 0777
			files["."] = file
		},
		"ancestor": func(files map[string]projectImageFile) {
			files["usr/local"] = projectImageFile{Kind: tar.TypeSymlink, Mode: 0777, Link: "/tmp"}
		},
		"spine mount": func(files map[string]projectImageFile) {
			files["mc/spine"] = projectImageFile{Kind: tar.TypeSymlink, Mode: 0777, Link: "/tmp"}
		},
		"identity mount": func(files map[string]projectImageFile) {
			files["mc/run.json"] = projectImageFile{Kind: tar.TypeSymlink, Mode: 0777, Link: "/etc/ld.so.preload"}
		},
		"session mount": func(files map[string]projectImageFile) {
			files["mc/session"] = projectImageFile{Kind: tar.TypeSymlink, Mode: 0777, Link: "/opt/culler-runtime"}
		},
		"source mount": func(files map[string]projectImageFile) {
			files["workspace/source"] = projectImageFile{Kind: tar.TypeSymlink, Mode: 0777, Link: "/opt/culler-runtime"}
		},
		"private runtime": func(files map[string]projectImageFile) {
			files["opt/culler-runtime"] = projectImageFile{Kind: tar.TypeDir, Mode: 0755}
		},
		"preload": func(files map[string]projectImageFile) {
			files["etc/ld.so.preload"] = projectImageFile{Kind: tar.TypeReg, Mode: 0644}
		},
		"library shadow": func(files map[string]projectImageFile) {
			files["usr/lib/aarch64-linux-gnu/glibc-hwcaps/custom/libc.so.6"] = projectImageFile{Kind: tar.TypeReg, Mode: 0755}
		},
		"account": func(files map[string]projectImageFile) {
			file := files["etc/passwd"]
			file.Body = append(file.Body, []byte("extra:x:10001:10001::/home/extra:/bin/sh\n")...)
			files["etc/passwd"] = file
		},
		"group member": func(files map[string]projectImageFile) {
			file := files["etc/group"]
			file.Body = bytes.ReplaceAll(file.Body, []byte("mc-completion:x:10001:"), []byte("mc-completion:x:10001:mc-model"))
			files["etc/group"] = file
		},
		"library cache": func(files map[string]projectImageFile) {
			file := files["etc/ld.so.cache"]
			file.Body = testLibraryCache([][2]string{{"libc.so.6", "/opt/other/libc.so.6"}})
			files["etc/ld.so.cache"] = file
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			base, candidate := projectPlatformFixture(), projectPlatformFixture()
			mutate(candidate)
			if err := compareProjectPlatform(base, candidate); err == nil {
				t.Fatal("a changed platform passed the image check")
			}
		})
	}
}
