package asset

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestMeasurementSourceReferencesAreExclusiveAndStable(t *testing.T) {
	t.Parallel()
	tagID, instanceID := uuid.New(), uuid.New()
	tag := TagSource(tagID)
	if err := tag.Validate(); err != nil || tag.Key() != "tag:"+tagID.String() {
		t.Fatalf("Tag source = %#v, %v", tag, err)
	}
	output := PluginOutputSource(instanceID, "today.electrical_energy_kwh")
	if err := output.Validate(); err != nil || output.Key() != "plugin_output:"+instanceID.String()+":today.electrical_energy_kwh" {
		t.Fatalf("Plugin source = %#v, %v", output, err)
	}
	invalid := []SourceReference{{}, {Kind: SourceTag}, {Kind: SourceTag, TagID: tagID, PluginInstanceID: instanceID}, {Kind: SourcePluginOutput, PluginInstanceID: instanceID}, {Kind: SourcePluginOutput, TagID: tagID, PluginInstanceID: instanceID, OutputKey: "power"}}
	for _, source := range invalid {
		if err := source.Validate(); !errors.Is(err, ErrInvalidBinding) {
			t.Errorf("Validate(%#v) = %v", source, err)
		}
	}
}
