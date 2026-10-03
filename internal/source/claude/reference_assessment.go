package claude

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/leonhfr/glean/internal/mdlink"
	"github.com/leonhfr/glean/internal/model"
	native "github.com/leonhfr/glean/internal/model/claude"
)

func assessReferences(source fs.FS, capability model.Capability) ([]model.Finding, error) {
	documents, err := referenceDocuments(source, capability)
	if err != nil {
		return nil, err
	}

	var findings []model.Finding
	for _, filename := range slices.Sorted(maps.Keys(documents)) {
		for _, link := range mdlink.Local([]byte(documents[filename])) {
			finding, err := assessLocalReference(source, capability, filename, link)
			if err != nil {
				return nil, err
			}

			findings = append(findings, finding)
		}
	}

	return findings, nil
}

func referenceDocuments(source fs.FS, capability model.Capability) (map[string]string, error) {
	var document string
	switch definition := capability.Definition.(type) {
	case native.Skill:
		document = definition.Document

	case native.Agent:
		document = definition.Document

	case native.Command:
		document = definition.Document

	default:
		return map[string]string{}, nil
	}

	filename := ""
	if len(capability.Payload.Entrypoints) > 0 {
		filename = capability.Payload.Entrypoints[0]
	}

	documents := map[string]string{filename: document}
	for _, directory := range capability.Payload.Assets {
		if err := readReferenceTree(source, directory, documents); err != nil {
			return nil, fmt.Errorf("inspect Markdown payload: %w", err)
		}
	}

	return documents, nil
}

func assessLocalReference(source fs.FS, capability model.Capability, filename string, link mdlink.Link) (model.Finding, error) {
	finding := capabilityFinding(capability, "LOCAL_REFERENCE", model.FindingUnknown, "local link", "Link requiredness and final retained-tree presence are unverified.")
	detail := &model.LocalReference{Source: filename, Line: link.Line, RawDestination: link.Raw}
	finding.LocalReference = detail
	if filename == "" {
		finding.Code = "REFERENCE_CONTEXT"
		finding.Message = "Inline Markdown has no filesystem document base; native relative-link resolution remains unverified."
		return finding, nil
	}

	finding.Locations = []model.Location{{Path: filename}}
	detail.Boundary = "."
	resolved := path.Join(path.Dir(filename), link.Path)
	detail.ResolvedDestination = resolved
	if invalidReference(link, resolved) {
		finding.Code = "REFERENCE_OUTSIDE_SOURCE"
		finding.Message = "Invalid or out-of-source link cannot be inspected through the approved source root."
		return finding, nil
	}

	info, err := fs.Stat(source, resolved)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		exists := false
		detail.Exists = &exists
		finding.Status = model.FindingMissing
		finding.Message = "Local link destination is absent; requiredness remains unknown."

	case err != nil:
		return model.Finding{}, fmt.Errorf("inspect local reference: %w", err)

	default:
		exists := info.Mode().IsRegular() || info.IsDir()
		detail.Exists = &exists
		if !exists {
			finding.Status = model.FindingMissing
		} else {
			finding.Status = model.FindingVerified
			finding.Message = "Destination exists inside the plugin tree; verify it remains after unselected entrypoints are removed."
		}
	}

	finding.Reference = resolved
	return finding, nil
}

func readReferenceTree(source fs.FS, directory string, documents map[string]string) error {
	return fs.WalkDir(source, directory, func(filename string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if filename == ".git" && entry.IsDir() {
			return fs.SkipDir
		}

		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(filename), ".md") {
			return nil
		}

		if _, exists := documents[filename]; exists {
			return nil
		}

		data, err := fs.ReadFile(source, filename)
		if err == nil {
			documents[filename] = string(data)
		}

		return err
	})
}

func invalidReference(link mdlink.Link, resolved string) bool {
	return link.Invalid || strings.HasPrefix(link.Path, "/") || strings.ContainsAny(link.Path, "\\\x00") || !fs.ValidPath(resolved) || resolved == ".git" || strings.HasPrefix(resolved, ".git/")
}
