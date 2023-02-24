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
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/containernetworking/cni/pkg/skel"
	types040 "github.com/containernetworking/cni/pkg/types/040"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/testutils"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

/***********************************************************************************************************************
 * Tests
 **********************************************************************************************************************/

var _ = Describe("Aos Vlan", func() {
	const ifName string = "eth0"
	var originalNS ns.NetNS
	var err error

	BeforeEach(func() {
		originalNS, err = testutils.NewNS()

		Expect(err).NotTo(HaveOccurred())

		err = originalNS.Do(func(ns.NetNS) (err error) {
			defer GinkgoRecover()

			dummy := netlink.Dummy{
				LinkAttrs: netlink.LinkAttrs{
					Name: ifName,
				},
			}

			err = netlink.LinkAdd(&dummy)
			Expect(err).NotTo(HaveOccurred())

			addr, err := netlink.ParseAddr("172.17.0.1/16")
			Expect(err).NotTo(HaveOccurred())

			err = netlink.AddrAdd(&dummy, addr)
			Expect(err).NotTo(HaveOccurred())

			_, err = netlink.LinkByName(ifName)
			Expect(err).NotTo(HaveOccurred())

			err = netlink.LinkSetUp(&dummy)
			Expect(err).NotTo(HaveOccurred())

			err = execCmd("ip", "route", "add", "default", "via", "172.17.0.1", "dev", ifName)
			Expect(err).NotTo(HaveOccurred())

			_, err = createBridge("br0", "22.2.0.1/16")
			Expect(err).NotTo(HaveOccurred())

			return err
		})
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		err := netns.DeleteNamed(filepath.Base(originalNS.Path()))
		Expect(err).NotTo(HaveOccurred())
	})

	It("aos-vlan add/check/delete", func() {
		conf := `
			{
			   "name": "mynet",
			   "cniVersion": "0.4.0",
			   "type": "aos-vlan",
			   "master": "br0",
			   "vlanId": 100,
			   "ifName": "aos-vlan"
		   }`

		args := &skel.CmdArgs{
			ContainerID: "dummy",
			Netns:       "dummy",
			IfName:      "aos-vlan",
			StdinData:   []byte(conf),
		}

		err = originalNS.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			result, _, err := testutils.CmdAddWithArgs(args, func() (err error) {
				return cmdAdd(args)
			})
			Expect(err).NotTo(HaveOccurred())

			r, err := types040.GetResult(result)
			Expect(err).NotTo(HaveOccurred())

			Expect(len(r.Interfaces)).To(Equal(1))
			Expect(r.Interfaces[0].Name).To(Equal("aos-vlan"))

			Expect(strings.Compare(r.Interfaces[0].Mac, "")).Should(BeNumerically("==", 1))

			link, err := netlink.LinkByName("aos-vlan")
			Expect(err).NotTo(HaveOccurred())
			Expect(link.Attrs().Flags & net.FlagUp).To(Equal(net.FlagUp))

			hwaddr, err := net.ParseMAC(r.Interfaces[0].Mac)
			Expect(err).NotTo(HaveOccurred())
			Expect(link.Attrs().HardwareAddr).To(Equal(hwaddr))

			_, _, err = testutils.CmdAddWithArgs(args, func() (err error) {
				return cmdAdd(args)
			})
			Expect(err).NotTo(HaveOccurred())

			return err
		})
		Expect(err).NotTo(HaveOccurred())

		err = originalNS.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			err = testutils.CmdCheckWithArgs(args, func() error {
				return cmdCheck(args)
			})
			Expect(err).NotTo(HaveOccurred())

			return err
		})

		err = originalNS.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			err = testutils.CmdDelWithArgs(args, func() error {
				return cmdDel(args)
			})
			Expect(err).NotTo(HaveOccurred())

			_, err = netlink.LinkByName("aos-vlan")
			Expect(err).NotTo(HaveOccurred())

			return err
		})
		Expect(err).NotTo(HaveOccurred())
	})

	It("aos-vlan master interface name is missing in the configuration", func() {
		conf := `
			{
			   "name": "mynet",
			   "cniVersion": "0.4.0",
			   "type": "aos-vlan",
			   "master": "",
			   "vlanId": 100,
			   "ifName": "aos-vlan"
		   }`

		args := &skel.CmdArgs{
			ContainerID: "dummy",
			Netns:       "dummy",
			IfName:      "aos-vlan",
			StdinData:   []byte(conf),
		}

		err = originalNS.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			_, _, err := testutils.CmdAddWithArgs(args, func() (err error) {
				return cmdAdd(args)
			})
			Expect(err).To(HaveOccurred())

			return err
		})
		Expect(err).To(HaveOccurred())
	})

	It("aos-vlan master link is down", func() {
		conf := `
			{
			   "name": "mynet",
			   "cniVersion": "0.4.0",
			   "type": "aos-vlan",
			   "master": "br0",
			   "vlanId": 100,
			   "ifName": "aos-vlan"
		   }`

		args := &skel.CmdArgs{
			ContainerID: "dummy",
			Netns:       "dummy",
			IfName:      "aos-vlan",
			StdinData:   []byte(conf),
		}

		err = originalNS.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			dummy, err := netlink.LinkByName(ifName)
			Expect(err).NotTo(HaveOccurred())

			err = netlink.LinkSetDown(dummy)
			Expect(err).NotTo(HaveOccurred())

			_, _, err = testutils.CmdAddWithArgs(args, func() (err error) {
				return cmdAdd(args)
			})
			Expect(err).To(HaveOccurred())

			return err
		})
		Expect(err).To(HaveOccurred())
	})
})

func createBridge(brName string, brIP string) (bridge *netlink.Bridge, err error) {
	err = execCmd("ip", "link", "add", "name", brName, "type", "bridge")
	if err != nil {
		return nil, err
	}

	err = execCmd("ip", "link", "set", brName, "up")
	if err != nil {
		return nil, err
	}

	err = execCmd("ip", "addr", "add", "dev", brName, brIP)
	if err != nil {
		return nil, err
	}

	return bridgeByName(brName)
}

func execCmd(bin string, args ...string) (err error) {
	output, err := exec.Command(bin, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("CMD %s, err: %s, output: %s", strings.Join(args, " "), err, string(output))
	}

	return nil
}

func bridgeByName(name string) (br *netlink.Bridge, err error) {
	l, err := netlink.LinkByName(name)
	if err != nil {
		return nil, fmt.Errorf("could not lookup %q: %v", name, err)
	}
	br, ok := l.(*netlink.Bridge)
	if !ok {
		return nil, fmt.Errorf("%q already exists but is not a bridge", name)
	}
	return br, nil
}
