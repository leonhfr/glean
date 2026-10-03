package claude_test

import (
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/leonhfr/glean/internal/model"
	"github.com/leonhfr/glean/internal/source/claude"
	"github.com/leonhfr/glean/internal/system"
)

func TestPluginLocalReferences(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		"skills/review/SKILL.md":       {Data: []byte("[present](refs/a%20b.txt?mode=1#part)\n[missing](missing.txt)\n[external](../../shared.txt)\n[escape](../../../outside.txt)\n[encoded escape](%2e%2e/%2e%2e/%2e%2e/outside.txt)\n[metadata](../../.git/config)\n[invalid](bad%ZZ)\n")},
		"skills/review/refs/a b.txt":   {Data: []byte("asset")},
		"skills/review/refs/nested.md": {Data: []byte("[nested missing](missing.txt)\n")},
		"shared.txt":                   {Data: []byte("shared")},
	}
	plugin, err := claude.ReadPlugin(agentSystem(source), "requested", "resolved")
	require.NoError(t, err)
	require.Len(t, plugin.Findings, 8)
	for _, finding := range plugin.Findings {
		require.NotNil(t, finding.LocalReference)
		assert.Equal(t, model.CapabilityID{Kind: model.KindSkill, Name: "review"}, finding.Capability)
		assert.Nil(t, finding.LocalReference.Required)
		assert.Equal(t, ".", finding.LocalReference.Boundary)
	}

	present := plugin.Findings[0]
	assert.Equal(t, model.FindingVerified, present.Status)
	assert.Equal(t, "refs/a%20b.txt?mode=1#part", present.LocalReference.RawDestination)
	assert.Equal(t, "skills/review/refs/a b.txt", present.LocalReference.ResolvedDestination)
	require.NotNil(t, present.LocalReference.Exists)
	assert.True(t, *present.LocalReference.Exists)
	assert.Equal(t, 1, present.LocalReference.Line)
	assert.Equal(t, model.FindingMissing, plugin.Findings[1].Status)
	assert.False(t, *plugin.Findings[1].LocalReference.Exists)
	assert.Equal(t, "LOCAL_REFERENCE", plugin.Findings[2].Code)
	assert.Equal(t, model.FindingVerified, plugin.Findings[2].Status)
	assert.True(t, *plugin.Findings[2].LocalReference.Exists)
	for _, finding := range plugin.Findings[3:7] {
		assert.Equal(t, "REFERENCE_OUTSIDE_SOURCE", finding.Code)
		assert.Nil(t, finding.LocalReference.Exists)
	}

	assert.Equal(t, "skills/review/refs/nested.md", plugin.Findings[7].LocalReference.Source)
	assert.Equal(t, "skills/review/refs/missing.txt", plugin.Findings[7].Reference)
}

func TestFlatAndInlineMarkdownReferenceBoundaries(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath:    {Data: []byte(`{"name":"tools","commands":{"inline":{"content":"[local](support.txt)"},"file":{"source":"./commands/file.md"}}}`)},
		"commands/file.md":     {Data: []byte("[sibling](support.txt)\n")},
		"commands/support.txt": {Data: []byte("support")},
		"agents/review.md":     {Data: []byte("[sibling](support.txt)\n")},
		"agents/support.txt":   {Data: []byte("support")},
	}
	plugin, err := claude.ReadPlugin(agentSystem(source), "requested", "resolved")
	require.NoError(t, err)
	require.Len(t, plugin.Findings, 3)
	for _, finding := range plugin.Findings {
		require.NotNil(t, finding.LocalReference)
		assert.Nil(t, finding.LocalReference.Required)
		if finding.Capability.Name == "inline" {
			assert.Equal(t, "REFERENCE_CONTEXT", finding.Code)
			assert.Equal(t, model.FindingUnknown, finding.Status)
			assert.Empty(t, finding.LocalReference.ResolvedDestination)
			assert.Nil(t, finding.LocalReference.Exists)
		} else {
			assert.Equal(t, "LOCAL_REFERENCE", finding.Code)
			assert.Equal(t, model.FindingVerified, finding.Status)
			assert.True(t, *finding.LocalReference.Exists)
		}
	}
}

func TestReferenceInspectionFailureDiscardsInventory(t *testing.T) {
	t.Parallel()
	failure := errors.New("reference stat failed")
	closed := 0
	source := referenceFailureFS{MapFS: fstest.MapFS{
		"skills/review/SKILL.md":  {Data: []byte("[asset](asset.txt)\n")},
		"skills/review/asset.txt": {Data: []byte("asset")},
	}, failure: failure}
	sys := &system.Fake{OpenRootHandler: func(string) (system.Root, error) {
		return &system.FakeRoot{Path: "/source/tools", Source: source, CloseHandler: func() error { closed++; return nil }}, nil
	}}
	plugin, err := claude.ReadPlugin(sys, "requested", "resolved")
	require.ErrorIs(t, err, failure)
	assert.Empty(t, plugin)
	assert.Equal(t, 1, closed)
}

type referenceFailureFS struct {
	fstest.MapFS
	failure error
}

func (f referenceFailureFS) Stat(name string) (fs.FileInfo, error) {
	if name == "skills/review/asset.txt" {
		return nil, f.failure
	}

	return fs.Stat(f.MapFS, name)
}
