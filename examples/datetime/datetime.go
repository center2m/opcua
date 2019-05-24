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
	endpoint := flag.String("endpoint", "opc.tcp://localhost:4840", "OPC UA Endpoint URL")
	flag.BoolVar(&debug.Enable, "debug", false, "enable debug logging")
	flag.Parse()
	log.SetFlags(0)

	c := opcua.NewClient(*endpoint)
	if err := c.Connect(); err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	v, err := c.Node(ua.NewNumericNodeID(0, 2258)).Value()
	switch {
	case err != nil:
		log.Fatal(err)
	case v == nil:
		log.Print("v == nil")
	default:
		log.Print(v.Value)
	}
}
