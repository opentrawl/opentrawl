package photocard

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/luna"
	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type generatedObjectSchema struct {
	Type                 string                          `json:"type"`
	Properties           map[string]generatedFieldSchema `json:"properties"`
	Required             []string                        `json:"required"`
	AdditionalProperties bool                            `json:"additionalProperties"`
}

type generatedFieldSchema struct {
	Type                 string                          `json:"type,omitempty"`
	Properties           map[string]generatedFieldSchema `json:"properties,omitempty"`
	Required             []string                        `json:"required,omitempty"`
	AdditionalProperties *bool                           `json:"additionalProperties,omitempty"`
	Items                *generatedFieldSchema           `json:"items,omitempty"`
	Enum                 []string                        `json:"enum,omitempty"`
}

func DescriptionsRepairStructuredOutputSchema() (luna.StructuredOutputSchema, error) {
	encodedSchema, err := generateStructuredOutputJSONSchema((&cardwire.PhotoDescriptions{}).ProtoReflect().Descriptor())
	if err != nil {
		return luna.StructuredOutputSchema{}, err
	}
	return luna.NewStructuredOutputSchema(encodedSchema)
}

func PhotoTextStructuredOutputSchemaJSON() ([]byte, error) {
	return generateStructuredOutputJSONSchema((&cardwire.PhotoOpticalCharacterRecognition{}).ProtoReflect().Descriptor())
}

func PhotoTextVerificationStructuredOutputSchemaJSON() ([]byte, error) {
	return generateStructuredOutputJSONSchema((&cardwire.PhotoOpticalCharacterRecognitionVerification{}).ProtoReflect().Descriptor())
}

func PhotoCardSemanticSectionsStructuredOutputSchemaJSON() ([]byte, error) {
	return generateStructuredOutputJSONSchema((&cardwire.PhotoCardSemanticSections{}).ProtoReflect().Descriptor())
}

func generateStructuredOutputJSONSchema(messageDescriptor protoreflect.MessageDescriptor) ([]byte, error) {
	properties, required, err := generateMessageProperties(messageDescriptor)
	if err != nil {
		return nil, err
	}
	return json.Marshal(generatedObjectSchema{
		Type:                 "object",
		Properties:           properties,
		Required:             required,
		AdditionalProperties: false,
	})
}

func generateMessageProperties(messageDescriptor protoreflect.MessageDescriptor) (map[string]generatedFieldSchema, []string, error) {
	properties := make(map[string]generatedFieldSchema, messageDescriptor.Fields().Len())
	required := make([]string, 0, messageDescriptor.Fields().Len())
	for fieldIndex := 0; fieldIndex < messageDescriptor.Fields().Len(); fieldIndex++ {
		fieldDescriptor := messageDescriptor.Fields().Get(fieldIndex)
		fieldSchema, err := generateFieldSchema(fieldDescriptor)
		if err != nil {
			return nil, nil, fmt.Errorf("field %s: %w", fieldDescriptor.FullName(), err)
		}
		fieldName := fieldDescriptor.JSONName()
		properties[fieldName] = fieldSchema
		required = append(required, fieldName)
	}
	sort.Strings(required)
	return properties, required, nil
}

func generateFieldSchema(fieldDescriptor protoreflect.FieldDescriptor) (generatedFieldSchema, error) {
	if fieldDescriptor.IsMap() || fieldDescriptor.IsExtension() || fieldDescriptor.ContainingOneof() != nil {
		return generatedFieldSchema{}, errors.New("maps, extensions and oneofs are not supported by the PhotoCard schema generator")
	}
	if fieldDescriptor.IsList() {
		itemSchema, err := generateSingularFieldSchema(fieldDescriptor)
		if err != nil {
			return generatedFieldSchema{}, err
		}
		return generatedFieldSchema{Type: "array", Items: &itemSchema}, nil
	}
	return generateSingularFieldSchema(fieldDescriptor)
}

func generateSingularFieldSchema(fieldDescriptor protoreflect.FieldDescriptor) (generatedFieldSchema, error) {
	switch fieldDescriptor.Kind() {
	case protoreflect.StringKind, protoreflect.BytesKind:
		return generatedFieldSchema{Type: "string"}, nil
	case protoreflect.BoolKind:
		return generatedFieldSchema{Type: "boolean"}, nil
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Uint32Kind, protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Uint64Kind, protoreflect.Fixed32Kind, protoreflect.Fixed64Kind, protoreflect.Sfixed32Kind, protoreflect.Sfixed64Kind:
		return generatedFieldSchema{Type: "integer"}, nil
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return generatedFieldSchema{Type: "number"}, nil
	case protoreflect.EnumKind:
		enumValues := fieldDescriptor.Enum().Values()
		allowedNames := make([]string, 0, enumValues.Len()-1)
		for valueIndex := 0; valueIndex < enumValues.Len(); valueIndex++ {
			value := enumValues.Get(valueIndex)
			if value.Number() != 0 {
				allowedNames = append(allowedNames, string(value.Name()))
			}
		}
		return generatedFieldSchema{Type: "string", Enum: allowedNames}, nil
	case protoreflect.MessageKind:
		properties, required, err := generateMessageProperties(fieldDescriptor.Message())
		if err != nil {
			return generatedFieldSchema{}, err
		}
		additionalProperties := false
		return generatedFieldSchema{
			Type:                 "object",
			Properties:           properties,
			Required:             required,
			AdditionalProperties: &additionalProperties,
		}, nil
	default:
		return generatedFieldSchema{}, fmt.Errorf("unsupported Protobuf kind %s", fieldDescriptor.Kind())
	}
}
