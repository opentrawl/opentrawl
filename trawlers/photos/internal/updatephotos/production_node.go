package updatephotos

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
)

type ProductionNodeName string

const (
	ProductionNodeSource                              ProductionNodeName = "source"
	ProductionNodeMediaAccess                         ProductionNodeName = "media-access"
	ProductionNodeCurrentMedia                        ProductionNodeName = "current-media"
	ProductionNodeKnownPlace                          ProductionNodeName = "known-place"
	ProductionNodeAppleReverseGeocoding               ProductionNodeName = "apple-reverse-geocoding"
	ProductionNodeAppleNearbyPlaces                   ProductionNodeName = "apple-nearby-places"
	ProductionNodeGeoapifyPhotographedPlaceCandidates ProductionNodeName = "geoapify-photographed-place-candidates"
	ProductionNodeComposeLocationEvidence             ProductionNodeName = "compose-location-evidence"
	ProductionNodePhotoTextExtraction                 ProductionNodeName = "photo-text-extraction"
	ProductionNodePhotoTextVerification               ProductionNodeName = "photo-text-verification"
	ProductionNodePhotoCard                           ProductionNodeName = "photo-card"
)

type ProductionNode struct {
	Name           ProductionNodeName
	Dependencies   []ProductionNodeName
	RequiresPhoto  bool
	Description    string
	debugOperation productionNodeDebugOperation
}

type productionNodeDebugOperation func(context.Context, *Runner, *photoAssetWorker, archive.PhotoUpdateAsset, string) (string, string, error)

var productionNodesInDependencyOrder = []ProductionNode{
	{Name: ProductionNodeSource, Description: "Index the current Apple Photos library"},
	{Name: ProductionNodeMediaAccess, Description: "Confirm the installed OpenTrawl app can read Apple Photos", debugOperation: debugMediaAccessNode},
	{Name: ProductionNodeCurrentMedia, Dependencies: []ProductionNodeName{ProductionNodeSource, ProductionNodeMediaAccess}, RequiresPhoto: true, Description: "Acquire the current edited and oriented image and immutable original facts", debugOperation: debugCurrentMediaNode},
	{Name: ProductionNodeKnownPlace, Dependencies: []ProductionNodeName{ProductionNodeSource}, RequiresPhoto: true, Description: "Match the capture coordinate against configured known places", debugOperation: debugLocationNodeOperation(ProductionNodeKnownPlace)},
	{Name: ProductionNodeAppleReverseGeocoding, Dependencies: []ProductionNodeName{ProductionNodeSource}, RequiresPhoto: true, Description: "Acquire or reuse Apple reverse-geocoding evidence", debugOperation: debugLocationNodeOperation(ProductionNodeAppleReverseGeocoding)},
	{Name: ProductionNodeAppleNearbyPlaces, Dependencies: []ProductionNodeName{ProductionNodeSource, ProductionNodeKnownPlace}, RequiresPhoto: true, Description: "Acquire or reuse Apple nearby-place evidence", debugOperation: debugLocationNodeOperation(ProductionNodeAppleNearbyPlaces)},
	{Name: ProductionNodeGeoapifyPhotographedPlaceCandidates, Dependencies: []ProductionNodeName{ProductionNodeSource, ProductionNodeKnownPlace}, RequiresPhoto: true, Description: "Acquire or reuse Geoapify candidates that may be depicted in the photo", debugOperation: debugLocationNodeOperation(ProductionNodeGeoapifyPhotographedPlaceCandidates)},
	{Name: ProductionNodeComposeLocationEvidence, Dependencies: []ProductionNodeName{ProductionNodeKnownPlace, ProductionNodeAppleReverseGeocoding, ProductionNodeAppleNearbyPlaces, ProductionNodeGeoapifyPhotographedPlaceCandidates}, RequiresPhoto: true, Description: "Compose retained known-place, Apple and Geoapify outputs into location evidence", debugOperation: debugLocationNodeOperation(ProductionNodeComposeLocationEvidence)},
	{Name: ProductionNodePhotoTextExtraction, Dependencies: []ProductionNodeName{ProductionNodeCurrentMedia}, RequiresPhoto: true, Description: "Extract comprehensive structured visible text with Luna", debugOperation: debugPhotoTextExtractionNode},
	{Name: ProductionNodePhotoTextVerification, Dependencies: []ProductionNodeName{ProductionNodeCurrentMedia, ProductionNodePhotoTextExtraction}, RequiresPhoto: true, Description: "Verify or correct retained structured visible text with Luna", debugOperation: debugPhotoTextVerificationNode},
	{Name: ProductionNodePhotoCard, Dependencies: []ProductionNodeName{ProductionNodeCurrentMedia, ProductionNodeComposeLocationEvidence, ProductionNodePhotoTextVerification}, RequiresPhoto: true, Description: "Build and store the typed PhotoCard from retained dependencies", debugOperation: debugPhotoCardNode},
}

func ProductionNodesInDependencyOrder() []ProductionNode {
	nodes := append([]ProductionNode(nil), productionNodesInDependencyOrder...)
	for index := range nodes {
		nodes[index].Dependencies = append([]ProductionNodeName(nil), nodes[index].Dependencies...)
	}
	return nodes
}

func ParseProductionNodeName(value string) (ProductionNodeName, error) {
	value = strings.TrimSpace(value)
	for _, node := range productionNodesInDependencyOrder {
		if value == string(node.Name) {
			return node.Name, nil
		}
	}
	return "", fmt.Errorf("unknown Photos production node %q", value)
}

func productionNodeForName(name ProductionNodeName) (ProductionNode, bool) {
	for _, node := range productionNodesInDependencyOrder {
		if node.Name == name {
			return node, true
		}
	}
	return ProductionNode{}, false
}
