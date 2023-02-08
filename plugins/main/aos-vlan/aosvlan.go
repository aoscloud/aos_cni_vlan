// SPDX-License-Identifier: Apache-2.0
//
// Copyright (C) 2023 Renesas Electronics Corporation.
// Copyright (C) 2023 EPAM Systems, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"runtime"
	"syscall"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/containernetworking/cni/pkg/version"
	bv "github.com/containernetworking/plugins/pkg/utils/buildversion"
	"github.com/vishvananda/netlink"
)

/***********************************************************************************************************************
 * Types
 **********************************************************************************************************************/

type pluginConf struct {
	types.NetConf
	VlanId int    `json:"vlanId"`
	Master string `json:"master"`
	IfName string `json:"ifName"`
}

/***********************************************************************************************************************
 * Init
 **********************************************************************************************************************/

func init() {
	// this ensures that main runs only on main thread (thread group leader).
	// since namespace ops (unshare, setns) are done for a single thread, we
	// must ensure that the goroutine does not jump from OS thread to thread
	runtime.LockOSThread()
}

/***********************************************************************************************************************
 * Main
 **********************************************************************************************************************/

func main() {
	skel.PluginMain(cmdAdd, cmdCheck, cmdDel, version.All, bv.BuildString("aos-vlan"))
}

/***********************************************************************************************************************
 * Private
 **********************************************************************************************************************/

func cmdAdd(args *skel.CmdArgs) error {
	conf, result, err := parseConfig(args.StdinData)
	if err != nil {
		return err
	}

	vlan, vlanInterface, err := createVlan(conf)
	if err != nil {
		return err
	}

	if err := addVlanToBridge(conf, vlan); err != nil {
		return err
	}

	result.Interfaces = append(result.Interfaces, vlanInterface)

	return types.PrintResult(&result, conf.CNIVersion)
}

// This plugin does not implement the delete logic because it should only exist when the master interface exists.
// Therefore, it should be deleted by the user.
func cmdDel(args *skel.CmdArgs) error {
	return nil
}

func cmdCheck(args *skel.CmdArgs) error {
	conf, _, err := parseConfig(args.StdinData)
	if err != nil {
		return err
	}

	vlan, err := vlanByName(conf.IfName)
	if err != nil {
		return err
	}

	if vlan.VlanId != conf.VlanId {
		return fmt.Errorf("vlan link %s configured promisc is %d, current value is %d",
			conf.IfName, conf.VlanId, vlan.VlanId)
	}

	if vlan.Flags&net.FlagUp != net.FlagUp {
		return fmt.Errorf("vlan link %s is down", conf.IfName)
	}

	return nil
}

func addVlanToBridge(conf *pluginConf, vlan *netlink.Vlan) error {
	br, err := netlink.LinkByName(conf.Master)
	if err != nil {
		return fmt.Errorf("failed to lookup %q: %v", conf.Master, err)
	}

	// connect host vlan to the bridge
	if err := netlink.LinkSetMaster(vlan, br); err != nil {
		return fmt.Errorf("failed to connect %q to bridge %s: %v", vlan.Attrs().Name, br.Attrs().Name, err)
	}

	return nil
}

func createVlan(conf *pluginConf) (*netlink.Vlan, *current.Interface, error) {
	mIndex, err := getMasterInterfaceIndex()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to lookup master index %v", err)
	}

	vlan := &netlink.Vlan{
		LinkAttrs: netlink.LinkAttrs{
			Name:        conf.IfName,
			ParentIndex: mIndex,
		},
		VlanId: conf.VlanId,
	}

	if err := netlink.LinkAdd(vlan); err != nil && err != syscall.EEXIST {
		return nil, nil, fmt.Errorf("failed to create vlan: %v", err)
	}

	if err := netlink.LinkSetUp(vlan); err != nil {
		return nil, nil, fmt.Errorf("failed to create vlan: %v", err)
	}

	// Re-fetch link to read all attributes
	vlan, err = vlanByName(conf.IfName)
	if err != nil {
		return nil, nil, err
	}

	return vlan, &current.Interface{
		Name: vlan.Attrs().Name,
		Mac:  vlan.Attrs().HardwareAddr.String(),
	}, nil
}

func vlanByName(name string) (*netlink.Vlan, error) {
	l, err := netlink.LinkByName(name)
	if err != nil {
		return nil, fmt.Errorf("could not lookup %q: %v", name, err)
	}

	vlan, ok := l.(*netlink.Vlan)
	if !ok {
		return nil, fmt.Errorf("%q already exists but is not a vlan", name)
	}

	return vlan, nil
}

func parseConfig(bytes []byte) (*pluginConf, current.Result, error) {
	config := &pluginConf{}
	if err := json.Unmarshal(bytes, config); err != nil {
		return nil, current.Result{}, fmt.Errorf("failed to parse network configuration: %v", err)
	}

	if config.IfName == "" {
		return nil, current.Result{}, fmt.Errorf(
			"\"ifName\" field is required. It specifies VLAN interface name.")
	}

	if config.Master == "" {
		return nil, current.Result{}, fmt.Errorf(
			"\"master\" field is required. It specifies the master interface name for VLAN subnetwork.")
	}

	if config.VlanId < 0 || config.VlanId > 4094 {
		return nil, current.Result{}, fmt.Errorf("invalid VLAN ID %d (must be between 0 and 4095 inclusive)", config.VlanId)
	}

	// Parse previous result.
	var (
		result *current.Result = &current.Result{}
		err    error
	)

	if config.RawPrevResult != nil {
		if err = version.ParsePrevResult(&config.NetConf); err != nil {
			return nil, current.Result{}, fmt.Errorf("could not parse prevResult: %v", err)
		}

		result, err = current.NewResultFromResult(config.PrevResult)
		if err != nil {
			return nil, current.Result{}, fmt.Errorf("could not convert result to current version: %v", err)
		}
	}

	return config, *result, err
}

func getMasterInterfaceIndex() (index int, err error) {
	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		return index, err
	}

	for _, route := range routes {
		if route.Dst == nil {
			return route.LinkIndex, nil
		}
	}

	return index, fmt.Errorf("master index not found")
}
