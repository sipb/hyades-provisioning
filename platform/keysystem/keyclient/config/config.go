package config

import (
	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
	"io/ioutil"
)

type ConfigDownload struct {
	Type    string
	Name    string
	Path    string
	Refresh string
	Mode    string
}

type ConfigKey struct {
	Name      string
	Type      string
	Key       string
	Cert      string
	API       string
	InAdvance string `yaml:"in-advance"`
}

type Config struct {
	AuthorityPath string
	Keyserver     string
	KeyPath       string
	CertPath      string
	TokenPath     string
	TokenAPI      string
	Downloads     []ConfigDownload
	Keys          []ConfigKey
}

func LoadConfig(configpath string) (Config, error) {
	config := Config{}
	configdata, err := ioutil.ReadFile(configpath)
	if err != nil {
		return Config{}, errors.Wrap(err, "while loading configuration")
	}
	err = yaml.UnmarshalStrict(configdata, &config)
	if err != nil {
		return Config{}, errors.Wrap(err, "while decoding configuration")
	}
	return config, nil
}
