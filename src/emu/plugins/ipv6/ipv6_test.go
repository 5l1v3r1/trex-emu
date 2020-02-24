package ipv6

import (
	"emu/core"
	"encoding/binary"
	"external/google/gopacket"
	"external/google/gopacket/layers"
	"flag"
	"fmt"
	"net"
	"testing"
	"time"
)

var monitor int

type IcmpTestBase struct {
	testname     string
	monitor      bool
	match        uint8
	capture      bool
	duration     time.Duration
	clientsToSim int
	mcToSim      int
	flush        int
	cb           IcmpTestCb
	cbArg1       interface{}
	cbArg2       interface{}
}

type IcmpTestCb func(tctx *core.CThreadCtx, test *IcmpTestBase) int

func (o *IcmpTestBase) Run(t *testing.T, compare bool) {

	var simVeth VethIcmpSim
	var simrx core.VethIFSim
	simrx = &simVeth
	if o.match > 0 {
		simVeth.match = o.match
	}
	tctx, _ := createSimulationEnv(&simrx, o.clientsToSim, o.mcToSim, o)
	if o.cb != nil {
		o.cb(tctx, o)
	}
	m := false
	if monitor > 0 {
		m = true
	}
	tctx.Veth.SetDebug(m, o.capture)
	tctx.MainLoopSim(o.duration)
	defer tctx.Delete()
	var key core.CTunnelKey
	key.Set(&core.CTunnelData{Vport: 1, Vlans: [2]uint32{0x81000001, 0x81000002}})

	ns := tctx.GetNs(&key)
	if ns == nil {
		t.Fatalf(" can't find ns")
		return
	}
	nsplg := ns.PluginCtx.Get(IPV6_PLUG)
	if nsplg == nil {
		t.Fatalf(" can't find plugin")
	}
	icmpPlug := nsplg.Ext.(*PluginIpv6Ns)
	icmpPlug.cdbv.Dump()
	tctx.SimRecordAppend(icmpPlug.cdb.MarshalValues(false))
	tctx.GetCounterDbVec().Dump()

	if compare {
		tctx.SimRecordCompare(o.testname, t)
	}
}

func createSimulationEnv(simRx *core.VethIFSim, num int, mcSim int, test *IcmpTestBase) (*core.CThreadCtx, *core.CClient) {
	tctx := core.NewThreadCtx(0, 4510, true, simRx)
	var key core.CTunnelKey
	key.Set(&core.CTunnelData{Vport: 1, Vlans: [2]uint32{0x81000001, 0x81000002}})
	ns := core.NewNSCtx(tctx, &key)
	tctx.AddNs(&key, ns)
	for j := 0; j < num; j++ {
		a := uint8((j >> 8) & 0xff)
		b := uint8(j & 0xff)
		var dg core.Ipv4Key
		if num == 1 {
			dg = core.Ipv4Key{16, 0, 0, 2}
		} else {
			dg = core.Ipv4Key{16, 1, a, b}
		}
		client := core.NewClient(ns, core.MACKey{0, 0, 1, 0, a, b},
			core.Ipv4Key{16, 0, a, b},
			core.Ipv6Key{0x20, 0x01, 0x0d, 0xb8, 0x00, 0x00, 0x00, 0x00,
				0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02},
			dg)
		ns.AddClient(client)
		if mcSim > 0 {
			ns.PluginCtx.CreatePlugins([]string{"ipv6"}, [][]byte{[]byte(`{"dmac" :[0, 0, 1, 0, 0, 0]  } `)})
		}
		client.PluginCtx.CreatePlugins([]string{"ipv6"}, [][]byte{})
		// simulate new API of RPC
		if test.flush > 0 {
			client.PluginCtx.CallCallback("OnPostCreate")
		}
	}
	tctx.RegisterParserCb("icmpv6")

	if mcSim > 0 {
		nsplg := ns.PluginCtx.Get(IPV6_PLUG)
		if nsplg == nil {
			panic(" can't find plugin")
		}
		nsPlug := nsplg.Ext.(*PluginIpv6Ns)

		vecIpv6 := []core.Ipv6Key{}
		fmt.Printf(" number of mc : %d \n", mcSim)
		for j := 0; j < mcSim; j++ {
			vecIpv6 = append(vecIpv6, core.Ipv6Key{0xff, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, uint8(((j >> 8) & 0xff)), uint8(j), 0})
		}

		nsPlug.mld.addMc(vecIpv6)
	}
	return tctx, nil
}

type VethIcmpSim struct {
	cnt   uint8
	match uint8
	tctx  *core.CThreadCtx
}

func (o *VethIcmpSim) ProcessTxToRx(m *core.Mbuf) *core.Mbuf {
	m.FreeMbuf()
	return nil
}

type IcmpQueryCtx struct {
	tctx  *core.CThreadCtx
	timer core.CHTimerObj
	cnt   uint16
	match uint8
}

func (o *IcmpQueryCtx) OnEvent(a, b interface{}) {
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{FixLengths: false, ComputeChecksums: false}

	var raw []byte

	switch o.match {
	case 0:
		gopacket.SerializeLayers(buf, opts,
			&layers.Ethernet{
				SrcMAC:       net.HardwareAddr{0, 0, 0, 2, 0, 0},
				DstMAC:       net.HardwareAddr{0, 0, 1, 0, 0, 0},
				EthernetType: layers.EthernetTypeDot1Q,
			},
			&layers.Dot1Q{
				Priority:       uint8(0),
				VLANIdentifier: uint16(1),
				Type:           layers.EthernetTypeDot1Q,
			},
			&layers.Dot1Q{
				Priority:       uint8(0),
				VLANIdentifier: uint16(2),
				Type:           layers.EthernetTypeIPv6,
			},

			&layers.IPv6{
				Version:      6,
				TrafficClass: 0,
				FlowLabel:    0,
				Length:       8,
				NextHeader:   layers.IPProtocolICMPv6,
				HopLimit:     64,
				SrcIP:        net.IP{0x20, 0x01, 0x0d, 0xb8, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
				DstIP: net.IP{0x20, 0x01, 0x0d, 0xb8, 0x00, 0x00, 0x00, 0x00,
					0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02},
			},

			&layers.ICMPv6{TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0)},

			&layers.ICMPv6Echo{Identifier: 0x1234,
				SeqNumber: 0x4567 + o.cnt},

			gopacket.Payload([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}),
		)
		pkt := buf.Bytes()
		off := 14 + 8
		ipv6 := layers.IPv6Header(pkt[off : off+40])
		ipv6.SetPyloadLength(uint16(len(pkt) - off - 40))

		cs := layers.PktChecksumTcpUdpV6(pkt[off+40:], 0, ipv6, 0, uint8(layers.IPProtocolICMPv6))
		binary.BigEndian.PutUint16(pkt[off+42:off+44], cs)

		raw = buf.Bytes()

	case 1:
		gopacket.SerializeLayers(buf, opts,
			&layers.Ethernet{
				SrcMAC:       net.HardwareAddr{0, 0, 0, 2, 0, 0},
				DstMAC:       net.HardwareAddr{0, 0, 1, 0, 0, 0},
				EthernetType: layers.EthernetTypeDot1Q,
			},
			&layers.Dot1Q{
				Priority:       uint8(0),
				VLANIdentifier: uint16(1),
				Type:           layers.EthernetTypeDot1Q,
			},
			&layers.Dot1Q{
				Priority:       uint8(0),
				VLANIdentifier: uint16(2),
				Type:           layers.EthernetTypeIPv6,
			},

			&layers.IPv6{
				Version:      6,
				TrafficClass: 0,
				FlowLabel:    0,
				Length:       8,
				NextHeader:   layers.IPProtocolICMPv6,
				HopLimit:     64,
				SrcIP:        net.IP{0x20, 0x01, 0x0d, 0xb8, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
				DstIP: net.IP{0x20, 0x01, 0x0d, 0xb8, 0x00, 0x00, 0x00, 0x00,
					0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02},
			},

			&layers.ICMPv6{TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0)},

			&layers.ICMPv6Echo{Identifier: 0x1234,
				SeqNumber: 0x4567 + o.cnt},

			gopacket.Payload([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}),
		)
		//pkt := buf.Bytes()

		pkt := buf.Bytes()
		off := 14 + 8

		hopByHop := []byte{58, 0, 0, 0, 1, 2, 3, 4}

		pkt2 := []byte{}
		pkt2 = append(pkt2, pkt[0:off+40]...)
		pkt2 = append(pkt2, hopByHop[:]...)
		pkt2 = append(pkt2, pkt[off+40:]...)

		ipv6 := layers.IPv6Header(pkt2[off : off+40])
		ipv6[6] = 0
		ipv6.SetPyloadLength(uint16(len(pkt2) - off - 40))

		binary.BigEndian.PutUint16(pkt2[off+50:off+52], 0)
		cs := layers.PktChecksumTcpUdpV6(pkt2[off+48:], 0, ipv6, 8, 58)
		binary.BigEndian.PutUint16(pkt2[off+50:off+52], cs)
		raw = pkt2
	case 2:

		gopacket.SerializeLayers(buf, opts,
			&layers.Ethernet{
				SrcMAC:       net.HardwareAddr{0, 0, 0, 2, 0, 0},
				DstMAC:       net.HardwareAddr{0, 0, 1, 0, 0, 0},
				EthernetType: layers.EthernetTypeDot1Q,
			},
			&layers.Dot1Q{
				Priority:       uint8(0),
				VLANIdentifier: uint16(1),
				Type:           layers.EthernetTypeDot1Q,
			},
			&layers.Dot1Q{
				Priority:       uint8(0),
				VLANIdentifier: uint16(2),
				Type:           layers.EthernetTypeIPv6,
			},

			&layers.IPv6{
				Version:      6,
				TrafficClass: 0,
				FlowLabel:    0,
				Length:       8,
				NextHeader:   layers.IPProtocolIPv6HopByHop,
				HopLimit:     1,
				SrcIP:        net.IP{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
				DstIP:        net.IP{0xff, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x16},
			},
			gopacket.Payload([]byte{0x3a, 00, 5, 2, 0, 0, 0, 0,
				0x82, 00, 00, 00,
				0x1e, 0x80, 0, 0,
				0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
				0x02, 0x10, 0, 0,
			}),
		)

		pkt := buf.Bytes()
		off := 14 + 8
		ipv6 := layers.IPv6Header(pkt[off : off+40])
		ipv6.SetPyloadLength(uint16(len(pkt) - off - 40))

		binary.BigEndian.PutUint16(pkt[off+50:off+52], 0)
		cs := layers.PktChecksumTcpUdpV6(pkt[off+48:], 0, ipv6, 8, 58)
		binary.BigEndian.PutUint16(pkt[off+50:off+52], cs)
		raw = pkt
	case 3:

		gopacket.SerializeLayers(buf, opts,
			&layers.Ethernet{
				SrcMAC:       net.HardwareAddr{0, 0, 0, 2, 0, 0},
				DstMAC:       net.HardwareAddr{0, 0, 1, 0, 0, 0},
				EthernetType: layers.EthernetTypeDot1Q,
			},
			&layers.Dot1Q{
				Priority:       uint8(0),
				VLANIdentifier: uint16(1),
				Type:           layers.EthernetTypeDot1Q,
			},
			&layers.Dot1Q{
				Priority:       uint8(0),
				VLANIdentifier: uint16(2),
				Type:           layers.EthernetTypeIPv6,
			},

			&layers.IPv6{
				Version:      6,
				TrafficClass: 0,
				FlowLabel:    0,
				Length:       8,
				NextHeader:   layers.IPProtocolIPv6HopByHop,
				HopLimit:     1,
				SrcIP:        net.IP{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
				DstIP:        net.IP{0xff, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02},
			},
			gopacket.Payload([]byte{0x3a, 00, 5, 2, 0, 0, 0, 0,
				0x82, 00, 00, 00,
				0x0e, 0x80, 0, 0,
				0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
			}),
		)

		pkt := buf.Bytes()
		off := 14 + 8
		ipv6 := layers.IPv6Header(pkt[off : off+40])
		ipv6.SetPyloadLength(uint16(len(pkt) - off - 40))

		binary.BigEndian.PutUint16(pkt[off+50:off+52], 0)
		cs := layers.PktChecksumTcpUdpV6(pkt[off+48:], 0, ipv6, 8, 58)
		binary.BigEndian.PutUint16(pkt[off+50:off+52], cs)
		raw = pkt

	case 4:
		// router adv

		gopacket.SerializeLayers(buf, opts,
			&layers.Ethernet{
				SrcMAC:       net.HardwareAddr{0, 0, 0, 2, 0, 0},
				DstMAC:       net.HardwareAddr{0x33, 0x33, 0, 0, 0, 1},
				EthernetType: layers.EthernetTypeDot1Q,
			},
			&layers.Dot1Q{
				Priority:       uint8(0),
				VLANIdentifier: uint16(1),
				Type:           layers.EthernetTypeDot1Q,
			},
			&layers.Dot1Q{
				Priority:       uint8(0),
				VLANIdentifier: uint16(2),
				Type:           layers.EthernetTypeIPv6,
			},

			&layers.IPv6{
				Version:      6,
				TrafficClass: 0,
				FlowLabel:    0,
				Length:       8,
				NextHeader:   layers.IPProtocolICMPv6,
				HopLimit:     255,
				SrcIP:        net.IP{0xfe, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xff, 0xfe, 0xf5, 0x00, 0x00},
				DstIP:        net.IP{0xff, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
			},

			&layers.ICMPv6{TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeRouterAdvertisement, 0)},

			&layers.ICMPv6RouterAdvertisement{
				HopLimit:       64,
				Flags:          0xc0,
				RouterLifetime: 1800,
				ReachableTime:  0,
				RetransTimer:   0,
			},

			gopacket.Payload([]byte{0x01, 0x01, 0xc2, 0x00, 0x54, 0xf5, 0x00, 0x00}),
			gopacket.Payload([]byte{0x05, 0x01, 0x00, 0x00, 0x00, 0x00, 0x05, 0xdc}),

			gopacket.Payload([]byte{0x03, 0x04, 0x40, 0xc0, 0x00, 0x27, 0x8d, 0x00, 0x00, 0x09, 0x3a, 0x80, 0x00, 0x00, 0x00, 0x00,
				0x20, 0x01, 0x0d, 0xb8, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}),
		)

		pkt := buf.Bytes()
		off := 14 + 8
		ipv6Optionsize := 0
		icmppyof := off + 40 + ipv6Optionsize

		ipv6 := layers.IPv6Header(pkt[off : off+40])
		ipv6.SetPyloadLength(uint16(len(pkt) - off - 40))

		binary.BigEndian.PutUint16(pkt[icmppyof+2:icmppyof+4], 0)
		cs := layers.PktChecksumTcpUdpV6(pkt[icmppyof:], 0, ipv6, 0, 58)
		binary.BigEndian.PutUint16(pkt[icmppyof+2:icmppyof+4], cs)
		raw = pkt

	}

	o.cnt += 1

	m := o.tctx.MPool.Alloc(uint16(256))
	m.SetVPort(1)
	m.Append(raw)
	o.tctx.Veth.OnRx(m)

	timerw := o.tctx.GetTimerCtx()
	ticks := timerw.DurationToTicks(10 * time.Second)
	timerw.StartTicks(&o.timer, ticks)
}

func Cb4(tctx *core.CThreadCtx, test *IcmpTestBase) int {
	timerw := tctx.GetTimerCtx()
	ticks := timerw.DurationToTicks(1 * time.Second)
	var arpctx IcmpQueryCtx
	arpctx.match = test.match
	arpctx.cnt = 0xabcd
	arpctx.timer.SetCB(&arpctx, test.cbArg1, test.cbArg2)
	arpctx.tctx = tctx
	timerw.StartTicks(&arpctx.timer, ticks)
	return 0
}

/*TestPluginIcmp1 - does not answer to default gateway, should repeat query */
func TestPluginIcmpv6_1(t *testing.T) {
	a := &IcmpTestBase{
		testname:     "icmpv6_1",
		monitor:      false,
		match:        0,
		capture:      true,
		duration:     1 * time.Minute,
		clientsToSim: 1,
		cb:           Cb4,
	}
	a.Run(t, true)
}

func TestPluginIcmpv6_2(t *testing.T) {
	a := &IcmpTestBase{
		testname:     "icmpv6_2",
		monitor:      false,
		match:        1,
		capture:      true,
		duration:     1 * time.Minute,
		clientsToSim: 1,
		cb:           Cb4,
	}
	a.Run(t, true) // the timestamp making a new json due to the timestamp. skip the it
}

func TestPluginMldv2_1(t *testing.T) {
	a := &IcmpTestBase{
		testname:     "mld2_1",
		monitor:      false,
		match:        2,
		capture:      true,
		duration:     1 * time.Minute,
		clientsToSim: 1,
		mcToSim:      3,
		cb:           Cb4,
	}
	a.Run(t, true) // the timestamp making a new json due to the timestamp. skip the it
}

func TestPluginMldv2_2(t *testing.T) {
	a := &IcmpTestBase{
		testname:     "mld2_2",
		monitor:      false,
		match:        2,
		capture:      true,
		duration:     20 * time.Second,
		clientsToSim: 1,
		mcToSim:      1000,
		cb:           Cb4,
	}
	a.Run(t, true) // the timestamp making a new json due to the timestamp. skip the it
}

func TestPluginMldv1_1(t *testing.T) {
	a := &IcmpTestBase{
		testname:     "mld1_1",
		monitor:      false,
		match:        3,
		capture:      true,
		duration:     1 * time.Minute,
		clientsToSim: 1,
		mcToSim:      3,
		cb:           Cb4,
	}
	a.Run(t, true) // the timestamp making a new json due to the timestamp. skip the it

}

type ipv6RpcCtx struct {
	tctx  *core.CThreadCtx
	timer core.CHTimerObj
	test  *IcmpTestBase
}

func rpcQueue(tctx *core.CThreadCtx, test *IcmpTestBase) int {
	timerw := tctx.GetTimerCtx()
	ticks := timerw.DurationToTicks(50 * time.Second)
	var tstctx ipv6RpcCtx
	tstctx.timer.SetCB(&tstctx, test.cbArg1, test.cbArg2)
	tstctx.tctx = tctx
	tstctx.test = test
	timerw.StartTicks(&tstctx.timer, ticks)
	return 0
}

func Ipv6SA(s string) string {
	ip := net.ParseIP(s)
	if len(ip) != 16 {
		panic(" not ipv6 addr")
	}
	return fmt.Sprintf("%v", []byte(ip))
}

func (o *ipv6RpcCtx) OnEvent(a, b interface{}) {
	fmt.Printf("add request %v %v \n", a, b)
	if a.(int) == 2 {

		buf := gopacket.NewSerializeBuffer()
		opts := gopacket.SerializeOptions{FixLengths: true,
			ComputeChecksums: true}

		gopacket.SerializeLayers(buf, opts,
			&layers.Ethernet{
				SrcMAC:       net.HardwareAddr{0, 0, 0, 2, 0, 0},
				DstMAC:       net.HardwareAddr{0x01, 0x00, 0x5e, 0x00, 0x00, 0x01},
				EthernetType: layers.EthernetTypeDot1Q,
			},
			&layers.Dot1Q{
				Priority:       uint8(0),
				VLANIdentifier: uint16(1),
				Type:           layers.EthernetTypeDot1Q,
			},
			&layers.Dot1Q{
				Priority:       uint8(0),
				VLANIdentifier: uint16(2),
				Type:           layers.EthernetTypeIPv4,
			},

			&layers.IPv4{Version: 4, IHL: 6, TTL: 1, Id: 0xcc,
				SrcIP:    net.IPv4(16, 0, 0, 10),
				DstIP:    net.IPv4(224, 0, 0, 1),
				Length:   44,
				Protocol: layers.IPProtocolIGMP,
				Options: []layers.IPv4Option{{ /* router alert */
					OptionType:   0x94,
					OptionData:   []byte{0, 0},
					OptionLength: 4},
				}},

			gopacket.Payload([]byte{0x11, 0x18, 0xec, 0xd3, 0x00, 0x00, 0x00, 0x00, 0x02, 0x14, 0x00, 0x00}),
		)
		m := o.tctx.MPool.Alloc(uint16(256))
		m.SetVPort(1)
		m.Append(buf.Bytes())
		//core.PacketUtl("arp1", buf.Bytes())
		o.tctx.Veth.OnRx(m)

		r1 := fmt.Sprintf(`{"jsonrpc": "2.0",
		"method":"igmp_ns_remove",
		"params": {"tun": {"vport":1,"tci":[1,2]}, "vec": [ %s,%s ] },
		"id": 3 }`, Ipv6SA("FE08::01"), Ipv6SA("FE08::02"))

		o.tctx.Veth.AppendSimuationRPC([]byte(r1))
	}

	o.tctx.Veth.AppendSimuationRPC([]byte(`{"jsonrpc": "2.0", 
	"method":"ipv6_mld_ns_get_cfg", 
	"params": {"tun": {"vport":1,"tci":[1,2]} }, 
	"id": 3 }`))

	o.tctx.Veth.AppendSimuationRPC([]byte(`{"jsonrpc": "2.0", 
	"method":"ipv6_mld_ns_set_cfg", 
	"params": {"tun": {"vport":1,"tci":[1,2]}, "mtu":10 }, "id": 3 }`))

	o.tctx.Veth.AppendSimuationRPC([]byte(`{"jsonrpc": "2.0", 
	"method":"ipv6_mld_ns_set_cfg", 
	"params": {"tun": {"vport":1,"tci":[1,2]}, "mtu":512 ,"dmac":[0,0,1,0,0,0] }, "id": 3 }`))

	o.tctx.Veth.AppendSimuationRPC([]byte(`{"jsonrpc": "2.0", 
	"method":"ipv6_mld_ns_get_cfg", 
	"params": {"tun": {"vport":1,"tci":[1,2]} }, 
	"id": 3 }`))

	o.tctx.Veth.AppendSimuationRPC([]byte(`{"jsonrpc": "2.0", 
	"method":"ipv6_ns_cnt", 
	"params": {"tun": {"vport":1,"tci":[1,2]}, "meta":true }, 
	"id": 3 }`))

	o.tctx.Veth.AppendSimuationRPC([]byte(`{"jsonrpc": "2.0", 
	"method":"ipv6_ns_cnt", 
	"params": {"tun": {"vport":1,"tci":[1,2]}, "meta":false, "zero":false }, 
	"id": 3 }`))

	o.tctx.Veth.AppendSimuationRPC([]byte(`{"jsonrpc": "2.0",
	"method":"ipv6_mld_ns_add",
	"params": {"tun": {"vport":1,"tci":[1,2]}, "vec": [ [251, 2, 0,0, 0,0,0,0, 0,0,0,0, 0,1,0,0 ] ] } ,
	"id": 3 }`))

	o.tctx.Veth.AppendSimuationRPC([]byte(`{"jsonrpc": "2.0",
	"method":"ipv6_mld_ns_add",
	"params": {"tun": {"vport":1,"tci":[1,2]}, "vec": [ [251, 2, 0,0, 0,0,0,0, 0,0,0,0, 0,1,0,0 ] ] } ,
	"id": 3 }`))

	o.tctx.Veth.AppendSimuationRPC([]byte(`{"jsonrpc": "2.0",
	"method":"ipv6_mld_ns_remove",
	"params": {"tun": {"vport":1,"tci":[1,2]}, "vec": [ [251, 2, 0,0, 0,0,0,0, 0,0,0,0, 0,1,0,0 ] ] } ,
	"id": 3 }`))

	o.tctx.Veth.AppendSimuationRPC([]byte(`{"jsonrpc": "2.0",
	"method":"ipv6_mld_ns_iter",
	"params": {"tun": {"vport":1,"tci":[1,2]}, "reset": true, "count" : 99},
	"id": 3 }`))
	o.tctx.Veth.AppendSimuationRPC([]byte(`{"jsonrpc": "2.0",
	"method":"ipv6_mld_ns_iter",
	"params": {"tun": {"vport":1,"tci":[1,2]}, "reset": false, "count" : 100},
	"id": 3 }`))

}

func TestPluginMldv2_rpc1(t *testing.T) {

	a := &IcmpTestBase{
		testname:     "mld2_rpc1",
		monitor:      false,
		match:        3,
		capture:      true,
		duration:     1 * time.Minute,
		clientsToSim: 1,
		mcToSim:      3,
		cb:           rpcQueue,
		cbArg1:       1,
	}
	a.Run(t, true) // the timestamp making a new json due to the timestamp. skip the it
}

func TestPluginNd_adv1(t *testing.T) {

	a := &IcmpTestBase{
		testname:     "ipv6nd_1",
		monitor:      false,
		match:        4,
		capture:      true,
		duration:     1 * time.Minute,
		clientsToSim: 1,
		cb:           Cb4,
		flush:        1,
	}
	a.Run(t, true) // the timestamp making a new json due to the timestamp. skip the it
}

func init() {
	flag.IntVar(&monitor, "monitor", 0, "monitor")
}
