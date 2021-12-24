package main

import (
	"github.com/nokia/srlinux-ndk-go/ndk"
	"github.com/rs/zerolog"
	"os/exec"
	"strings"
	"time"
    "os"
    "encoding/json"
)

type ConfigurationManager struct {
    vniConfigs map[string]VniConfig
	logger       *zerolog.Logger
}

func NewConfigurationManager(logger *zerolog.Logger) *ConfigurationManager {
    var c ConfigurationManager

	c.vniConfigs = make(map[string]VniConfig)
    c.logger = logger

    return &c
}

func (c *ConfigurationManager)processBgpConfig(agent *Agent, op ndk.SdkMgrOperation, bgpc string) {

	agent.TerminateChildProcess()

	for i := 1; i < 10; i++ {
		_, err := os.Stat("/var/run/netns/srbase-default")
		if err != nil {
			c.logger.Info().Msg("Waiting for namespace to be ready")
			time.Sleep(1 * time.Second)
			continue
		}
		break
	}

	cmd := exec.Command("ip", "netns", "exec", "srbase-default", "/opt/static-vxlan-agent/bin/static-vxlan-agent", "-c")

	agent.SetChildProcess(cmd)
	agent.SendToChildProcess("bgpc", bgpc)
	// No need to send Configs right now, since this will get done on commit.end
}

func (c *ConfigurationManager)processVniConfig(op ndk.SdkMgrOperation , conf string, keys []string) {
	var rawjson map[string]interface{}
	json.Unmarshal([]byte(conf), &rawjson);

    vrf := keys[0]
	admin_state := rawjson["admin_state"].(string)
	vni := rawjson["vni"].(map[string]interface{})["value"].(string);
	evi := rawjson["evi"].(map[string]interface{})["value"].(string);

	var vniConfig VniConfig
	if c, found := c.vniConfigs[vrf]; found {
		vniConfig = c
	}
	vniConfig.AdminState = admin_state
	vniConfig.Vni = vni
	vniConfig.Evi = evi
	c.vniConfigs[vrf] = vniConfig

	c.logger.Info().Msgf("Received VNI Config for VRF %s. admin_state: %s, vni: %s, evi: %s", vrf, admin_state, vni, evi)
	// No need to send Configs right now, since this will get done on commit.end
}

func (c *ConfigurationManager)processCommitEnd(agent *Agent) {
	str, _ := json.Marshal(c.vniConfigs)
	c.logger.Info().Msgf("Configs: %s", string(str))
	agent.SendToChildProcess("vrf", string(str))
}

func (c *ConfigurationManager)processVtepConfig(op ndk.SdkMgrOperation, conf string, keys []string) {
	vrf := keys[0]
	vtep := keys[2]

	if ((op == ndk.SdkMgrOperation_Create) || (op == ndk.SdkMgrOperation_Change)) {
		var vniConfig VniConfig
		if item, found := c.vniConfigs[vrf]; found {
			vniConfig = item
		}
		v := Vtep{Address: vtep}
		vniConfig.Vteps = append(vniConfig.Vteps, v)
		c.vniConfigs[vrf] = vniConfig
	} else if op == ndk.SdkMgrOperation_Delete {
		if item, found := c.vniConfigs[vrf]; found {
			for i, v := range item.Vteps {
				if v.Address == vtep {
					item.Vteps = append(item.Vteps[:i], item.Vteps[i+1:]...)
					break
				}
			}
			c.vniConfigs[vrf] = item
		}
	}

	c.logger.Info().Msgf("Received VTep Config for VRF %s. vtep: %s", vrf, vtep)
	// No need to send Configs right now, since this will get done on commit.end
}

func (c *ConfigurationManager)processNotification(agent *Agent, n *ndk.ConfigNotification) {

	op := n.GetOp()
    conf := n.GetData().GetJson()
    key := n.GetKey().JsPath
	c.logger.Info().Msgf("Received notifications: %v", n)

    if key == ".network_instance.protocols.static_vxlan_agent" {
        c.processBgpConfig(agent, op, strings.ReplaceAll(conf,"\n",""))
	} else if key == ".network_instance.protocols.bgp_evpn.bgp_instance.static_vxlan_agent" {
		c.processVniConfig(op, strings.ReplaceAll(conf,"\n",""),  n.GetKey().Keys)
	} else if key == ".network_instance.protocols.bgp_evpn.bgp_instance.static_vxlan_agent.static_vtep" {
		c.processVtepConfig(op, strings.ReplaceAll(conf,"\n",""), n.GetKey().Keys)
	} else if key == ".commit.end" {
		c.processCommitEnd(agent)
	}
}
