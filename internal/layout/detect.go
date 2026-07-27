package layout

import (
	"sort"

	"github.com/louie/nomad-debug-helper/internal/bundle"
)

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

	notes := []string{
		"v0 accepts an unpacked debug output directory or a .tar.gz/.tgz archive.",
		"Unknown files remain visible in the raw inventory instead of failing parsing.",
	}
	kind := "nomad-debug-output-dir"
	if raw.SourceKind == bundle.SourceArchive {
		kind = "nomad-debug-output-archive"
		notes = append(notes, "Archive input is extracted to a temporary working directory for browsing.")
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
		Confidence:    confidence,
		MetadataFiles: metadataFiles,
		Counts:        countList,
		Notes:         notes,
	}
}
