package directory

const (
	SecretPath = "ldap"
	SecretKey  = "bind_password"
)

type TestRequest struct {
	Config       Config `json:"config"`
	BindPassword string `json:"bind_password"`
	TestUsername string `json:"test_username,omitempty"`
	TestPassword string `json:"test_password,omitempty"`
}

type TestReport struct {
	OK    bool   `json:"ok"`
	Probe Probe  `json:"probe"`
	Error string `json:"error,omitempty"`
}

func Report(p Probe, err error) TestReport {
	r := TestReport{OK: err == nil, Probe: p}
	if err != nil {
		r.Error = err.Error()
	}
	return r
}

func (c Config) Public() Config {
	c.BindPasswordRef = ""
	return c
}
