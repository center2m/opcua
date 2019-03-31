// Copyright 2018-2019 opcua authors. All rights reserved.
// Use of this source code is governed by a MIT-style license that can be
// found in the LICENSE file.

package opcua

import (
	"time"

	"github.com/gopcua/opcua/id"
	"github.com/gopcua/opcua/ua"
)

// Node is a high-level object to interact with a node in the
// address space. It provides common convenience functions to
// access and manipulate the common attributes of a node.
type Node struct {
	// ID is the node id of the node.
	ID *ua.NodeID

	c *Client
}

func (n *Node) String() string {
	return n.ID.String()
}

// NodeClass returns the node class attribute.
func (n *Node) NodeClass() (ua.NodeClass, error) {
	v, err := n.Attribute(ua.AttributeIDNodeClass)
	if err != nil {
		return 0, err
	}
	return ua.NodeClass(v.Int()), nil
}

// AccessLevel returns the access level of the node.
func (n *Node) AccessLevel() (ua.AccessLevelType, error) {
	v, err := n.Attribute(ua.AttributeIDAccessLevel)
	if err != nil {
		return 0, err
	}
	//fmt.Printf("value: %#v\n", v)
	return ua.AccessLevelType(v.Value.(byte)), nil
}

// BrowseName returns the browse name of the node.
func (n *Node) BrowseName() (*ua.QualifiedName, error) {
	v, err := n.Attribute(ua.AttributeIDBrowseName)
	if err != nil {
		return nil, err
	}
	return v.Value.(*ua.QualifiedName), nil
}

// Description returns the description of the node.
func (n *Node) Description() (*ua.LocalizedText, error) {
	v, err := n.Attribute(ua.AttributeIDDescription)
	if err != nil {
		return nil, err
	}
	return v.Value.(*ua.LocalizedText), nil
}

// DisplayName returns the display name of the node.
func (n *Node) DisplayName() (*ua.LocalizedText, error) {
	v, err := n.Attribute(ua.AttributeIDDisplayName)
	if err != nil {
		return nil, err
	}
	return v.Value.(*ua.LocalizedText), nil
}

// Value returns the value of the node.
func (n *Node) Value() (*ua.Variant, error) {
	return n.Attribute(ua.AttributeIDValue)
}

// Attribute returns the attribute of the node. with the given id.
func (n *Node) Attribute(attrID ua.AttributeID) (*ua.Variant, error) {
	rv := &ua.ReadValueID{NodeID: n.ID, AttributeID: attrID}
	req := &ua.ReadRequest{NodesToRead: []*ua.ReadValueID{rv}}
	res, err := n.c.Read(req)
	if err != nil {
		return nil, err
	}
	if len(res.Results) == 0 {
		// #188: we return StatusBadUnexpectedError because it is unclear, under what
		// circumstances the server would return no error and no results in the response
		return nil, ua.StatusBadUnexpectedError
	}
	value := res.Results[0].Value
	if res.Results[0].Status != ua.StatusOK {
		return value, res.Results[0].Status
	}
	return value, nil
}

func (n *Node) Attributes(attrID ...ua.AttributeID) ([]*ua.DataValue, error) {
	req := &ua.ReadRequest{}
	for _, id := range attrID {
		rv := &ua.ReadValueID{NodeID: n.ID, AttributeID: id}
		req.NodesToRead = append(req.NodesToRead, rv)
	}
	res, err := n.c.Read(req)
	if err != nil {
		return nil, err
	}
	return res.Results, nil
}

func (n *Node) Children(refs uint32, mask ua.NodeClass) ([]*Node, error) {
	if refs == 0 {
		refs = id.HierarchicalReferences
	}
	return n.ReferencedNodes(refs, ua.BrowseDirectionForward, mask, true)
}

func (n *Node) ReferencedNodes(refs uint32, dir ua.BrowseDirection, mask ua.NodeClass, includeSubtypes bool) ([]*Node, error) {
	if refs == 0 {
		refs = id.References
	}
	var nodes []*Node
	res, err := n.References(refs, dir, mask, includeSubtypes)
	if err != nil {
		return nil, err
	}
	for _, r := range res {
		nodes = append(nodes, n.c.Node(r.NodeID.NodeID))
	}
	return nodes, nil
}

// References returns all references for the node.
// todo(fs): this is not complete since it only returns the
// todo(fs): top-level reference at this point.
func (n *Node) References(refType uint32, dir ua.BrowseDirection, mask ua.NodeClass, includeSubtypes bool) ([]*ua.ReferenceDescription, error) {
	if refType == 0 {
		refType = id.References
	}
	if mask == 0 {
		mask = ua.NodeClassAll
	}

	desc := &ua.BrowseDescription{
		NodeID:          n.ID,
		BrowseDirection: dir,
		ReferenceTypeID: ua.NewNumericNodeID(0, refType),
		IncludeSubtypes: includeSubtypes,
		NodeClassMask:   uint32(mask),
		ResultMask:      uint32(ua.BrowseResultMaskAll),
	}

	req := &ua.BrowseRequest{
		View: &ua.ViewDescription{
			ViewID:    ua.NewTwoByteNodeID(0),
			Timestamp: time.Now(),
		},
		RequestedMaxReferencesPerNode: 0,
		NodesToBrowse:                 []*ua.BrowseDescription{desc},
	}

	resp, err := n.c.Browse(req)
	if err != nil {
		return nil, err
	}
	return n.browseNext(resp.Results)
}

func (n *Node) browseNext(results []*ua.BrowseResult) ([]*ua.ReferenceDescription, error) {
	refs := results[0].References
	for len(results[0].ContinuationPoint) > 0 {
		req := &ua.BrowseNextRequest{
			ContinuationPoints:        [][]byte{results[0].ContinuationPoint},
			ReleaseContinuationPoints: false,
		}
		resp, err := n.c.BrowseNext(req)
		if err != nil {
			return nil, err
		}
		results = resp.Results
		refs = append(refs, results[0].References...)
	}
	return refs, nil
}
