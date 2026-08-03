package updatephotos

import (
	"fmt"
	"strings"
)

type ProductionNodeName string

const (
	ProductionNodeSource                              ProductionNodeName = "source"
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
	Name                              ProductionNodeName
	Dependencies                      []ProductionNodeName
	RequiresPhoto                     bool
	RetainedOutputInspectionAvailable bool
}

var productionNodesInDependencyOrder = []ProductionNode{
	{Name: ProductionNodeSource, RetainedOutputInspectionAvailable: true},
	{Name: ProductionNodeCurrentMedia, Dependencies: []ProductionNodeName{ProductionNodeSource}, RequiresPhoto: true, RetainedOutputInspectionAvailable: true},
	{Name: ProductionNodeKnownPlace, Dependencies: []ProductionNodeName{ProductionNodeSource}, RequiresPhoto: true, RetainedOutputInspectionAvailable: true},
	{Name: ProductionNodeAppleReverseGeocoding, Dependencies: []ProductionNodeName{ProductionNodeSource}, RequiresPhoto: true, RetainedOutputInspectionAvailable: true},
	{Name: ProductionNodeAppleNearbyPlaces, Dependencies: []ProductionNodeName{ProductionNodeSource, ProductionNodeKnownPlace}, RequiresPhoto: true, RetainedOutputInspectionAvailable: true},
	{Name: ProductionNodeGeoapifyPhotographedPlaceCandidates, Dependencies: []ProductionNodeName{ProductionNodeSource, ProductionNodeKnownPlace}, RequiresPhoto: true, RetainedOutputInspectionAvailable: true},
	{Name: ProductionNodeComposeLocationEvidence, Dependencies: []ProductionNodeName{ProductionNodeKnownPlace, ProductionNodeAppleReverseGeocoding, ProductionNodeAppleNearbyPlaces, ProductionNodeGeoapifyPhotographedPlaceCandidates}, RequiresPhoto: true, RetainedOutputInspectionAvailable: true},
	{Name: ProductionNodePhotoTextExtraction, Dependencies: []ProductionNodeName{ProductionNodeCurrentMedia}, RequiresPhoto: true},
	{Name: ProductionNodePhotoTextVerification, Dependencies: []ProductionNodeName{ProductionNodeCurrentMedia, ProductionNodePhotoTextExtraction}, RequiresPhoto: true},
	{Name: ProductionNodePhotoCard, Dependencies: []ProductionNodeName{ProductionNodeCurrentMedia, ProductionNodeComposeLocationEvidence, ProductionNodePhotoTextVerification}, RequiresPhoto: true},
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
