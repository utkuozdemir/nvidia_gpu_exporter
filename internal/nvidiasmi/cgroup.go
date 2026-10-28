package nvidiasmi

import (
	"io/fs"
	"slices"
	"strings"
	"unicode/utf8"
)

// containerIDLength is the length of a full hexadecimal container ID.
const containerIDLength = 64

// containerScopePrefixes are the systemd scope and cgroup segment prefixes
// used by Docker, containerd, CRI-O, and Podman.
//
//nolint:gochecknoglobals // constant lookup table
var containerScopePrefixes = []string{
	"docker-",
	"cri-containerd-",
	"crio-",
	"libpod-payload-",
	"libpod-",
}

// ReadProcessIdentity reads `<pid>/cgroup` from procFS and returns the
// process's container ID or systemd service unit. Unreadable or missing files
// return empty strings.
func ReadProcessIdentity(procFS fs.FS, pid string) (string, string) {
	if procFS == nil || pid == "" || !fs.ValidPath(pid) {
		return "", ""
	}

	data, err := fs.ReadFile(procFS, pid+"/cgroup")
	if err != nil {
		return "", ""
	}

	return ParseCgroupIdentity(string(data))
}

// ParseCgroupIdentity extracts a 64-character hex container ID or a systemd
// `.service` unit from the contents of `/proc/<pid>/cgroup`.
//
// When a container ID is found, the systemd unit is left empty even if the
// container cgroup sits beneath a runtime daemon unit (such as
// `docker.service` under the `cgroupfs` driver) or a user manager unit
// (`user@1000.service` for rootless Podman).
func ParseCgroupIdentity(content string) (string, string) {
	var systemdUnit string

	for line := range strings.Lines(content) {
		hierarchyID, controllers, cgroupPath, ok := splitCgroupLine(line)
		if !ok {
			continue
		}

		if id := parsePathContainerID(cgroupPath); id != "" {
			return id, ""
		}

		if systemdUnit == "" && isSystemdHierarchy(hierarchyID, controllers) {
			systemdUnit = parsePathSystemdUnit(cgroupPath)
		}
	}

	return "", systemdUnit
}

// splitCgroupLine splits one `<hierarchy-ID>:<controller-list>:<cgroup-path>`
// line from `/proc/<pid>/cgroup`.
func splitCgroupLine(line string) (string, string, string, bool) {
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return "", "", "", false
	}

	hierarchyID, rest, ok := strings.Cut(line, ":")
	if !ok {
		return "", "", "", false
	}

	controllers, cgroupPath, ok := strings.Cut(rest, ":")
	if !ok {
		return "", "", "", false
	}

	return hierarchyID, controllers, cgroupPath, true
}

// isSystemdHierarchy reports whether a `/proc/<pid>/cgroup` entry belongs to
// the cgroup v2 unified hierarchy (`0::`) or the cgroup v1 systemd hierarchy
// (`name=systemd`).
func isSystemdHierarchy(hierarchyID, controllers string) bool {
	return (hierarchyID == "0" && controllers == "") || controllers == "name=systemd"
}

// parsePathContainerID scans a cgroup path from innermost to outermost segment
// and returns the first 64-character hex container ID it finds.
func parsePathContainerID(cgroupPath string) string {
	for _, seg := range slices.Backward(strings.Split(cgroupPath, "/")) {
		if id := parseSegmentContainerID(seg); id != "" {
			return id
		}
	}

	return ""
}

// parseSegmentContainerID extracts a 64-character lowercase hex container ID
// from a single cgroup path segment, matching either a runtime-prefixed scope
// (`docker-<id>.scope`, `cri-containerd-<id>.scope`, `crio-<id>.scope`,
// `libpod-<id>.scope`, `libpod-payload-<id>`) or a bare 64-hex segment.
func parseSegmentContainerID(seg string) string {
	seg = strings.TrimSuffix(seg, ".scope")

	for _, prefix := range containerScopePrefixes {
		if rest, ok := strings.CutPrefix(seg, prefix); ok {
			if isLowerHex64(rest) {
				return rest
			}

			return ""
		}
	}

	if isLowerHex64(seg) {
		return seg
	}

	return ""
}

// parsePathSystemdUnit scans a cgroup path from innermost to outermost segment
// for the innermost systemd unit (`.service` or `.scope`). It returns the unit
// name only when that innermost unit is a `.service` other than the per-user
// systemd manager (`user@<uid>.service`).
func parsePathSystemdUnit(cgroupPath string) string {
	for _, seg := range slices.Backward(strings.Split(cgroupPath, "/")) {
		if strings.HasSuffix(seg, ".scope") {
			return ""
		}

		if strings.HasSuffix(seg, ".service") {
			if isUserManagerService(seg) || !utf8.ValidString(seg) {
				return ""
			}

			return seg
		}
	}

	return ""
}

// isUserManagerService reports whether seg is a systemd user manager unit such
// as `user@1000.service`.
func isUserManagerService(seg string) bool {
	uid, ok := strings.CutPrefix(seg, "user@")
	if !ok {
		return false
	}

	uid, ok = strings.CutSuffix(uid, ".service")
	if !ok || uid == "" {
		return false
	}

	for i := range len(uid) {
		if uid[i] < '0' || uid[i] > '9' {
			return false
		}
	}

	return true
}

// isLowerHex64 reports whether val is a 64-character lowercase hexadecimal
// string.
func isLowerHex64(val string) bool {
	if len(val) != containerIDLength {
		return false
	}

	for i := range len(val) {
		ch := val[i]
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}

	return true
}
