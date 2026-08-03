package updatephotos

import (
	"fmt"
	"strings"
)

type ProductionNodeName string

const (
	ProductionNodeSource                      ProductionNodeName = "source"
	ProductionNodeCurrentMedia                ProductionNodeName = "current-media"
	ProductionNodeImmutableOriginalImageFacts ProductionNodeName = "immutable-original-image-facts"
	ProductionNodeKnownPlace                  ProductionNodeName = "known-place"
	ProductionNodeAppleReverseGeocoding       ProductionNodeName = "apple-reverse-geocoding"
	ProductionNodeAppleNearbyPlaces           ProductionNodeName = "apple-nearby-places"
	ProductionNodeGeoapifyReverseGeocoding    ProductionNodeName = "geoapify-reverse-geocoding"
	ProductionNodeGeoapifyNearbyPlaces        ProductionNodeName = "geoapify-nearby-places"
	ProductionNodeComposeLocationEvidence     ProductionNodeName = "compose-location-evidence"
)

type ProductionNode struct {
	Name          ProductionNodeName
	Dependencies  []ProductionNodeName
	RequiresPhoto bool
}

var productionNodesInDependencyOrder = []ProductionNode{
	{Name: ProductionNodeSource},
	{Name: ProductionNodeCurrentMedia, Dependencies: []ProductionNodeName{ProductionNodeSource}, RequiresPhoto: true},
	{Name: ProductionNodeImmutableOriginalImageFacts, Dependencies: []ProductionNodeName{ProductionNodeSource}, RequiresPhoto: true},
	{Name: ProductionNodeKnownPlace, Dependencies: []ProductionNodeName{ProductionNodeSource}, RequiresPhoto: true},
	{Name: ProductionNodeAppleReverseGeocoding, Dependencies: []ProductionNodeName{ProductionNodeSource}, RequiresPhoto: true},
	{Name: ProductionNodeGeoapifyReverseGeocoding, Dependencies: []ProductionNodeName{ProductionNodeSource}, RequiresPhoto: true},
	{Name: ProductionNodeAppleNearbyPlaces, Dependencies: []ProductionNodeName{ProductionNodeSource, ProductionNodeKnownPlace}, RequiresPhoto: true},
	{Name: ProductionNodeGeoapifyNearbyPlaces, Dependencies: []ProductionNodeName{ProductionNodeSource, ProductionNodeKnownPlace}, RequiresPhoto: true},
	{Name: ProductionNodeComposeLocationEvidence, Dependencies: []ProductionNodeName{ProductionNodeKnownPlace, ProductionNodeAppleReverseGeocoding, ProductionNodeAppleNearbyPlaces, ProductionNodeGeoapifyReverseGeocoding, ProductionNodeGeoapifyNearbyPlaces}, RequiresPhoto: true},
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
