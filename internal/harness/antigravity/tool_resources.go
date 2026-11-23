package antigravity

import (
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

// Imports final tool resources into the owning worker's immutable store. The
// worker validates any path before reading, including symbolic link targets.
func (r *runtime) importToolContent(id string, content contentBlock, indices map[string]int) (domain.ContentPart, bool) {
	var none domain.ContentPart
	if r.options.ImportResource == nil {
		return none, false
	}
	if content.Type == "image" {
		index := indices["image"]
		if index >= domain.MaxMessageAttachments {
			return none, false
		}
		attachment, err := r.importImage(id, index, content)
		if err != nil {
			return none, false
		}
		indices["image"]++
		return domain.ContentPart{Kind: "image", Attachment: &attachment}, true
	}
	source := harness.OutputResource{Kind: "file", SessionID: r.session.ID, ItemID: id}
	switch content.Type {
	case "resource":
		resource := content.Resource
		if resource == nil || resource.Text == "" || len(resource.Text) > 8<<20 {
			return none, false
		}
		if resource.MIMEType != "" && !strings.HasPrefix(resource.MIMEType, "text/") {
			return none, false
		}
		source.Data = []byte(resource.Text)
		if uri, err := url.Parse(resource.URI); err == nil && uri.Path != "" {
			source.Name = clip(filepath.Base(uri.Path), 256)
		}
	case "resource_link":
		uri, err := url.Parse(content.URI)
		if err != nil || uri.Scheme != "file" || uri.Host != "" || uri.RawQuery != "" || uri.Fragment != "" || uri.User != nil ||
			!filepath.IsAbs(uri.Path) || strings.ContainsRune(uri.Path, 0) {
			return none, false
		}
		source.Path = uri.Path
		source.Name = filepath.Base(uri.Path)
		if strings.HasPrefix(content.MIMEType, "image/") {
			// An ACP link can point at an arbitrary file. It cannot claim the
			// worker's temporary-directory access for native image outputs.
			roots := append([]string{r.session.Context.CWD}, r.session.Context.AdditionalDirectories...)
			if !slices.ContainsFunc(roots, func(root string) bool {
				rel, err := filepath.Rel(root, uri.Path)
				return filepath.IsAbs(root) && err == nil && rel != "." && filepath.IsLocal(rel)
			}) {
				return none, false
			}
			source.Kind = "image"
		}
	default:
		return none, false
	}
	source.Index = indices[source.Kind]
	if source.Index >= domain.MaxMessageAttachments {
		return none, false
	}
	attachment, err := r.options.ImportResource(source)
	if err != nil {
		return none, false
	}
	indices[source.Kind]++
	return domain.ContentPart{Kind: source.Kind, Label: attachment.Name, Attachment: &attachment}, true
}
