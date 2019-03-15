// Copyright 2018-2019 opcua authors. All rights reserved.
// Use of this source code is governed by a MIT-style license that can be
// found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/gopcua/opcua"
	"github.com/gopcua/opcua/debug"
	"github.com/gopcua/opcua/ua"
)

type NodeDef struct {
	NodeID      *ua.NodeID
	Path        string
	DataType    string
	Writable    bool
	Description string
}

func join(a, b string) string {
	if a == "" {
		return b
	}
	return a + "." + b
}

func browse(n *opcua.Node, path string, level int) ([]NodeDef, error) {
	fmt.Printf("node:%s path:%q level:%d\n", n, path, level)
	if level > 10 {
		return nil, nil
	}
	nodeClass, err := n.NodeClass()
	if err != nil {
		return nil, err
	}
	browseName, err := n.BrowseName()
	if err != nil {
		return nil, err
	}
	descr, err := n.Description()
	if err != nil {
		return nil, err
	}
	// accessLevel, err := n.AccessLevel()
	// if err != nil {
	// 	return nil, err
	// }
	// writable := ua.AccessLevelType(accessLevel)&ua.AccessLevelTypeCurrentWrite == ua.AccessLevelTypeCurrentWrite
	writable := false

	path = join(path, browseName.Name)

	switch nodeClass {
	case ua.NodeClassObject:
		var nodes []NodeDef
		children, err := n.Children(0, 0)
		if err != nil {
			return nil, err
		}
		for _, cn := range children {
			childnodes, err := browse(cn, path, level+1)
			if err != nil {
				return nil, err
			}
			nodes = append(nodes, childnodes...)
		}
		return nodes, nil

	case ua.NodeClassVariable:
		return []NodeDef{
			{
				NodeID:      n.ID,
				Path:        path,
				Description: descr.Text,
				Writable:    writable,
				DataType:    "???",
			},
		}, nil

	}
	return nil, nil
}

func main() {
	endpoint := flag.String("endpoint", "opc.tcp://localhost:4840", "OPC UA Endpoint URL")
	nodeID := flag.String("node", "", "node id for the root node")
	flag.BoolVar(&debug.Enable, "debug", false, "enable debug logging")
	flag.Parse()
	log.SetFlags(0)

	c := opcua.NewClient(*endpoint)
	if err := c.Connect(); err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	id, err := ua.ParseNodeID(*nodeID)
	if err != nil {
		log.Fatalf("invalid node id: %s", err)
	}

	nodeList, err := browse(c.Node(id), "", 0)
	if err != nil {
		log.Fatal(err)
	}
	for _, s := range nodeList {
		fmt.Println(s)
	}
}
