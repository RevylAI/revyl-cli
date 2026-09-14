package build

import (
	"archive/tar"
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const maxArchiveLinkTargetBytes = 4096
const maxArchiveLinkExpansions = 128
const maxArchiveMembers = 100000

type archiveMember struct {
	name     string
	target   string
	isLink   bool
	hardLink bool
	isDir    bool
	mode     os.FileMode
}

type archiveLayout struct {
	members []archiveMember
	links   map[string]archiveMember
}

func normalizedArchivePath(value string) string {
	return strings.ReplaceAll(value, "\\", "/")
}

func archivePathKey(value string) string {
	return norm.NFD.String(cases.Fold().String(norm.NFD.String(value)))
}

func validateArchiveRelativePath(value string) error {
	if value == "" || strings.ContainsRune(value, '\x00') || strings.HasPrefix(value, "/") ||
		(len(value) >= 2 && value[1] == ':') {
		return fmt.Errorf("archive path must be relative and nonempty")
	}
	return nil
}

func resolveArchiveMember(name string, links map[string]archiveMember) (string, error) {
	pending := strings.Split(name, "/")
	var components []string
	expansions := 0
	for len(pending) > 0 {
		component := pending[0]
		pending = pending[1:]
		switch component {
		case "", ".":
			continue
		case "..":
			if len(components) == 0 {
				return "", fmt.Errorf("archive path escapes extraction directory")
			}
			components = components[:len(components)-1]
			continue
		}
		components = append(components, component)
		link, found := links[archivePathKey(strings.Join(components, "/"))]
		if !found {
			continue
		}
		expansions++
		if expansions > maxArchiveLinkExpansions {
			return "", fmt.Errorf("archive link chain is cyclic or exceeds expansion limit")
		}
		if link.hardLink {
			components = nil
		} else {
			components = components[:len(components)-1]
		}
		pending = append(strings.Split(link.target, "/"), pending...)
	}
	return strings.Join(components, "/"), nil
}

func validateArchiveMembers(members []archiveMember) (*archiveLayout, error) {
	links := make(map[string]archiveMember)
	seen := make(map[string]bool)
	for _, member := range members {
		if err := validateArchiveRelativePath(member.name); err != nil {
			return nil, fmt.Errorf("unsafe archive entry %q: %w", member.name, err)
		}
		if _, err := resolveArchiveMember(member.name, nil); err != nil {
			return nil, fmt.Errorf("unsafe archive entry %q: %w", member.name, err)
		}
		name := archivePathKey(path.Clean(member.name))
		if name == "." && !member.isDir {
			return nil, fmt.Errorf("unsafe archive entry %q: only directories may name the extraction root", member.name)
		}
		if seen[name] {
			return nil, fmt.Errorf("unsafe archive entry %q: duplicate normalized member path", member.name)
		}
		seen[name] = true
		if member.isLink {
			if len(member.target) > maxArchiveLinkTargetBytes {
				return nil, fmt.Errorf("unsafe archive link %q: target exceeds size limit", member.name)
			}
			if err := validateArchiveRelativePath(member.target); err != nil {
				return nil, fmt.Errorf("unsafe archive link %q: %w", member.name, err)
			}
			links[name] = member
		}
	}
	destinations := make(map[string]bool)
	for _, member := range members {
		if member.isLink {
			parent := path.Dir(member.name)
			resolvedParent, err := resolveArchiveMember(parent, links)
			if err != nil || archivePathKey(path.Clean(resolvedParent)) != archivePathKey(parent) {
				return nil, fmt.Errorf("unsafe archive link %q: link parent is aliased", member.name)
			}
		}
		resolved, err := resolveArchiveMember(member.name, links)
		if err != nil {
			return nil, fmt.Errorf("unsafe archive entry %q: %w", member.name, err)
		}
		if member.isLink {
			continue
		}
		key := archivePathKey(resolved)
		if _, exists := destinations[key]; exists {
			return nil, fmt.Errorf("unsafe archive entry %q: duplicate resolved member path", member.name)
		}
		destinations[key] = member.isDir
	}
	for _, member := range members {
		if member.isLink {
			destinations[archivePathKey(path.Clean(member.name))] = false
		}
	}
	for name := range destinations {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if isDir, exists := destinations[parent]; exists && !isDir {
				return nil, fmt.Errorf("unsafe archive entry %q: file used as a parent directory", name)
			}
		}
	}
	return &archiveLayout{members: members, links: links}, nil
}

func validateZipArchive(reader *zip.Reader) (*archiveLayout, error) {
	if len(reader.File) > maxArchiveMembers {
		return nil, fmt.Errorf("unsafe archive: member count exceeds %d", maxArchiveMembers)
	}
	members := make([]archiveMember, 0, len(reader.File))
	for _, entry := range reader.File {
		mode := entry.Mode()
		if !mode.IsRegular() && !mode.IsDir() && mode&os.ModeSymlink == 0 {
			return nil, fmt.Errorf("unsafe archive entry %q: unsupported filesystem entry", entry.Name)
		}
		member := archiveMember{name: normalizedArchivePath(entry.Name), isDir: entry.FileInfo().IsDir(), mode: mode}
		if entry.Mode()&os.ModeSymlink != 0 {
			stream, err := entry.Open()
			if err != nil {
				return nil, fmt.Errorf("failed to read archive link: %w", err)
			}
			target, readErr := io.ReadAll(io.LimitReader(stream, maxArchiveLinkTargetBytes+1))
			if err := errors.Join(readErr, stream.Close()); err != nil {
				return nil, fmt.Errorf("failed to read archive link: %w", err)
			}
			member.isLink = true
			member.target = normalizedArchivePath(string(target))
		}
		members = append(members, member)
	}
	return validateArchiveMembers(members)
}

func validateTarArchive(reader *tar.Reader) (*archiveLayout, error) {
	var members []archiveMember
	for {
		entry, err := reader.Next()
		if err == io.EOF {
			return validateArchiveMembers(members)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read tar entry: %w", err)
		}
		if len(members) == maxArchiveMembers {
			return nil, fmt.Errorf("unsafe archive: member count exceeds %d", maxArchiveMembers)
		}
		switch entry.Typeflag {
		case tar.TypeReg, tar.TypeRegA, tar.TypeDir, tar.TypeSymlink, tar.TypeLink:
		default:
			return nil, fmt.Errorf("unsafe archive entry %q: unsupported filesystem entry", entry.Name)
		}
		members = append(members, archiveMember{
			name: normalizedArchivePath(entry.Name), target: normalizedArchivePath(entry.Linkname),
			isLink:   entry.Typeflag == tar.TypeSymlink || entry.Typeflag == tar.TypeLink,
			hardLink: entry.Typeflag == tar.TypeLink,
			isDir:    entry.Typeflag == tar.TypeDir,
			mode:     entry.FileInfo().Mode(),
		})
	}
}
