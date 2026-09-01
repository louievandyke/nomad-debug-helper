package layout

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/louie/nomad-debug-helper/internal/bundle"
)

// consulRootFiles are files that appear at the root of a `consul debug`
// bundle but not in a Nomad bundle. Presence of any one is sufficient to
// identify the bundle as Consul.
var consulRootFiles = map[string]struct{}{
	"agent.json":     {},
	"consul.log":     {},
	"host.json":      {},
	"listpeers.json": {},
	"ports.json":     {},
	"trace.out":      {},
}

func Detect(raw *bundle.Bundle) Info {
	counts := map[string]int{}
	metadataFiles := make([]string, 0, 8)

	for _, file := range raw.Files {
		counts[string(file.Category)]++
		if file.Category == bundle.CategoryMetadata {
			metadataFiles = append(metadataFiles, file.RelPath)
		}
	}

	confidence := "low"
	if counts[string(bundle.CategoryMetadata)] > 0 {
		confidence = "medium"
	}
	if counts[string(bundle.CategoryLog)] > 0 || counts[string(bundle.CategoryEvent)] > 0 || counts[string(bundle.CategoryPprof)] > 0 || counts[string(bundle.CategorySnapshot)] > 0 {
		confidence = "high"
	}

	product := detectProduct(raw)

	notes := []string{
		"v0 accepts an unpacked debug output directory or a .tar.gz/.tgz archive.",
		"Unknown files remain visible in the raw inventory instead of failing parsing.",
	}

	var kind string
	switch {
	case product == "consul" && raw.SourceKind == bundle.SourceArchive:
		kind = "consul-debug-output-archive"
		notes = append(notes, "Archive input is extracted to a temporary working directory for browsing.")
	case product == "consul":
		kind = "consul-debug-output-dir"
	case raw.SourceKind == bundle.SourceArchive:
		kind = "nomad-debug-output-archive"
		notes = append(notes, "Archive input is extracted to a temporary working directory for browsing.")
	default:
		kind = "nomad-debug-output-dir"
	}

	if len(metadataFiles) == 0 {
		notes = append(notes, "No obvious top-level metadata files were detected.")
	}

	countList := make([]Count, 0, len(counts))
	for category, count := range counts {
		countList = append(countList, Count{
			Category: category,
			Count:    count,
		})
	}
	sort.Slice(countList, func(i, j int) bool {
		if countList[i].Count == countList[j].Count {
			return countList[i].Category < countList[j].Category
		}
		return countList[i].Count > countList[j].Count
	})

	return Info{
		Kind:          kind,
		Product:       product,
		Confidence:    confidence,
		MetadataFiles: metadataFiles,
		Counts:        countList,
		Notes:         notes,
	}
}

// detectProduct returns "consul" when root-level files unique to `consul debug`
// are present, and "nomad" otherwise.
func detectProduct(raw *bundle.Bundle) string {
	for _, file := range raw.Files {
		// Only examine files directly at the bundle root (no path separator).
		if strings.Contains(file.RelPath, "/") {
			continue
		}
		base := strings.ToLower(filepath.Base(file.RelPath))
		if _, ok := consulRootFiles[base]; ok {
			return "consul"
		}
	}
	return "nomad"
}
