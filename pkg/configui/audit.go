package configui

import (
	"strings"

	"github.com/grovetools/core/config"
)

// AuditSectionTitle labels the synthetic tree section that groups keys the
// config audit found in a layer file but that no known reader consumes.
const AuditSectionTitle = "keys nothing reads"

// AuditBadge returns the warning badge text rendered on audit rows, matching
// the FieldMeta.StatusBadge style ("⚠ DEPRECATED" etc.).
func AuditBadge(class config.AuditClass) string {
	switch class {
	case config.AuditOrphan:
		return "⚠ ORPHAN"
	case config.AuditUnknownNested:
		return "⚠ UNREAD"
	default:
		return ""
	}
}

// BuildAuditNodes builds the "keys nothing reads" section for one layer page:
// a single expanded header node whose children are the orphan and
// unknown-nested findings reported for that layer. The rows are selectable
// like schema rows so actions (info, delete-from-layer) work on them.
// Returns nil when the layer has no such findings.
func BuildAuditNodes(findings []config.AuditFinding, layered *config.LayeredConfig, layer config.ConfigSource) []*ConfigNode {
	header := &ConfigNode{
		Field: FieldMeta{
			Type:        FieldObject,
			Description: "Keys set in this layer that no known reader consumes.",
			Layer:       layer,
		},
		Key:          AuditSectionTitle,
		ActiveSource: layer,
		auditSection: true,
	}

	layerCfg := auditLayerConfig(layered, layer)
	for i := range findings {
		f := findings[i]
		if f.Layer != layer {
			continue
		}
		if f.Class != config.AuditOrphan && f.Class != config.AuditUnknownNested {
			continue
		}

		path := strings.Split(f.Key, ".")
		node := &ConfigNode{
			Field: FieldMeta{
				Path:  path,
				Type:  FieldString,
				Layer: layer,
			},
			Key:          f.Key,
			Depth:        1,
			Parent:       header,
			IsDynamic:    true,
			ActiveSource: f.Layer,
			Audit:        &findings[i],
		}
		// Best-effort value lookup: top-level orphans land in the layer's
		// Extensions map; unknown-nested keys are dropped by the decoder
		// and stay nil.
		if layerCfg != nil {
			node.Value = getConfigValueInterface(layerCfg, path)
		}
		if node.Value != nil {
			node.Field.Type = inferFieldType(node.Value)
		}
		header.Children = append(header.Children, node)
	}

	if len(header.Children) == 0 {
		return nil
	}
	return []*ConfigNode{header}
}

// auditLayerConfig returns the per-layer decoded config used to look up raw
// values for audit rows.
func auditLayerConfig(layered *config.LayeredConfig, layer config.ConfigSource) *config.Config {
	if layered == nil {
		return nil
	}
	switch layer {
	case config.SourceGlobal:
		return layered.Global
	case config.SourceEcosystem:
		return layered.Ecosystem
	case config.SourceProjectNotebook:
		return layered.ProjectNotebook
	case config.SourceProject:
		return layered.Project
	}
	return nil
}
