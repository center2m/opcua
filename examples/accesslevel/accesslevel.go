// Copyright 2018-2019 opcua authors. All rights reserved.
// Use of this source code is governed by a MIT-style license that can be
// found in the LICENSE file.

package main

import (
	"flag"
	"log"

	"github.com/center2m/opcua"
	"github.com/center2m/opcua/debug"
	"github.com/center2m/opcua/ua"
)

func main() {
	var (
		endpoint = flag.String("endpoint", "opc.tcp://localhost:4840", "OPC UA Endpoint URL")
		nodeID   = flag.String("node", "", "NodeID to read")
	)
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
		log.Fatal(err)
	}

	n := c.Node(id)
	accessLevel, err := n.AccessLevel()
	if err != nil {
		log.Fatal(err)
	}
	log.Print("AccessLevel: ", accessLevel)

	userAccessLevel, err := n.UserAccessLevel()
	if err != nil {
		log.Fatal(err)
	}
	log.Print("UserAccessLevel: ", userAccessLevel)

	v, err := n.Value()
	switch {
	case err != nil:
		log.Fatal(err)
	case v == nil:
		log.Print("v == nil")
	default:
		log.Print(v.Value)
	}
}
