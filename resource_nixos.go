package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/andrewchambers/terraform-provider-nix/nix"
	"github.com/hashicorp/terraform/helper/schema"
)

// A nixos server somewhere in the ether.
func resourceNixOS() *schema.Resource {
	return &schema.Resource{
		Create:        resourceNixOSCreateUpdate,
		Update:        resourceNixOSCreateUpdate,
		Read:          resourceNixOSRead,
		Delete:        resourceNixOSDelete,
		CustomizeDiff: resourceNixOSCustomizeDiff,

		Schema: map[string]*schema.Schema{
			"target_host": &schema.Schema{
				Type:     schema.TypeString,
				Required: true,
			},
			"target_user": &schema.Schema{
				Type:     schema.TypeString,
				Optional: true,
				Default:  "root",
			},
			"build_host": &schema.Schema{
				Type:     schema.TypeString,
				Optional: true,
				Default:  "localhost",
			},
			"nixos_config": &schema.Schema{
				Type:     schema.TypeString,
				Required: true,
			},
			"ssh_opts": &schema.Schema{
				Type:     schema.TypeString,
				Optional: true,
				Default:  "-o StrictHostKeyChecking=accept-new -o BatchMode=yes",
			},
			"nix_path": &schema.Schema{
				Type:     schema.TypeString,
				Optional: true,
			},
			"ssh_timeout": &schema.Schema{
				Type:     schema.TypeInt,
				Optional: true,
				Default:  180,
			},
			"collect_garbage": &schema.Schema{
				Type:     schema.TypeBool,
				Optional: true,
				Default:  true,
			},
			"nixos_system": &schema.Schema{
				Type:     schema.TypeString,
				Computed: true,
			},
			"pre_switch_hook": &schema.Schema{
				Type:      schema.TypeString,
				Optional:  true,
				Default:   "",
				Sensitive: true,
			},
			"post_switch_hook": &schema.Schema{
				Type:      schema.TypeString,
				Optional:  true,
				Default:   "",
				Sensitive: true,
			},
		},
	}
}

type nixosResourceConfig struct {
	TargetHost     string
	TargetUser     string
	BuildHost      string
	NixosConfig    string
	CollectGarbage bool
	NixPath        string
	SSHOpts        string
	PreSwitchHook  string
	PostSwitchHook string
	SSHTimeout     time.Duration
}

func (cfg *nixosResourceConfig) GetRebuildConfig() *nix.NixosRebuildConfig {
	return &nix.NixosRebuildConfig{
		TargetHost:     cfg.TargetHost,
		TargetUser:     cfg.TargetUser,
		BuildHost:      cfg.BuildHost,
		NixosConfig:    cfg.NixosConfig,
		NixPath:        cfg.NixPath,
		SSHOpts:        cfg.SSHOpts,
		PreSwitchHook:  cfg.PreSwitchHook,
		PostSwitchHook: cfg.PostSwitchHook,
	}
}

func getNixosConfig(d resourceLike) (nixosResourceConfig, error) {

	nixPath, ok := d.GetOk("nix_path")
	if !ok {
		nixPath = os.Getenv("NIX_PATH")
	}

	sshOpts, ok := d.GetOk("ssh_opts")
	if !ok {
		sshOpts = os.Getenv("NIX_SSHOPTS")
	}

	nixosConfig := d.Get("nixos_config").(string)

	nixosConfig, err := filepath.Abs(nixosConfig)
	if err != nil {
		return nixosResourceConfig{}, err
	}

	return nixosResourceConfig{
		TargetHost:     d.Get("target_host").(string),
		TargetUser:     d.Get("target_user").(string),
		BuildHost:      d.Get("build_host").(string),
		PreSwitchHook:  d.Get("pre_switch_hook").(string),
		PostSwitchHook: d.Get("post_switch_hook").(string),
		NixosConfig:    nixosConfig,
		NixPath:        nixPath.(string),
		SSHOpts:        sshOpts.(string),
		SSHTimeout:     time.Duration(d.Get("ssh_timeout").(int)) * time.Second,
		CollectGarbage: d.Get("collect_garbage").(bool),
	}, nil
}

func resourceNixOSCreateUpdate(d *schema.ResourceData, m interface{}) error {

	id := d.Id()
	if id == "" {
		d.SetId(randomID())
	}

	cfg, err := getNixosConfig(d)
	if err != nil {
		return err
	}

	rebuildCfg := cfg.GetRebuildConfig()

	err = nix.WaitForSSH(cfg.TargetUser, cfg.TargetHost, cfg.SSHOpts, cfg.SSHTimeout)
	if err != nil {
		return err
	}

	if cfg.CollectGarbage {
		err = nix.CollectGarbage(cfg.TargetUser, cfg.TargetHost, cfg.SSHOpts)
		if err != nil {
			return err
		}
	}

	if d.HasChange("nixos_system") || d.HasChange("target_host") || d.HasChange("pre_switch_hook") || d.HasChange("post_switch_hook") {
		err = nix.SwitchSystem(rebuildCfg)
		if err != nil {
			return err
		}
	}

	return resourceNixOSRead(d, m)
}

func resourceNixOSRead(d *schema.ResourceData, m interface{}) error {

	cfg, err := getNixosConfig(d)
	if err != nil {
		return err
	}
	rebuildCfg := cfg.GetRebuildConfig()

	currentSystem := "unknown"

	err = nix.WaitForSSH(cfg.TargetUser, cfg.TargetHost, cfg.SSHOpts, cfg.SSHTimeout)
	if err == nil {
		currentSystem, err = nix.CurrentSystem(rebuildCfg)
		if err != nil {
			return err
		}
	}

	err = d.Set("nixos_system", currentSystem)
	if err != nil {
		return err
	}

	return nil
}

func resourceNixOSDelete(d *schema.ResourceData, m interface{}) error {
	return nil
}

func resourceNixOSCustomizeDiff(d *schema.ResourceDiff, m interface{}) error {
	cfg, err := getNixosConfig(d)
	if err != nil {
		return err
	}
	rebuildCfg := cfg.GetRebuildConfig()

	desiredSystem, err := nix.BuildSystem(rebuildCfg)
	if err != nil {
		log.Printf("build failed, assuming this is because of generated configs. err=%s", err.Error())
		// If this really is an error, it will be picked up by the switch command.
		d.SetNewComputed("nixos_system")
		return nil
	}

	if d.Get("nixos_system").(string) != desiredSystem {
		d.SetNewComputed("nixos_system")
	}

	return nil
}
