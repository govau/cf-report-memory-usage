package main

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"code.cloudfoundry.org/cli/plugin"
	"github.com/olekukonko/tablewriter"
)

// simpleClient is a simple CloudFoundry client
type simpleClient struct {
	// API url, ie "https://api.system.example.com"
	API string

	// Authorization header, ie "bearer eyXXXXX"
	Authorization string

	// Quiet - if set don't print progress to stderr
	Quiet bool

	// Client - http.Client to use
	Client *http.Client
}

// Get makes a GET request, where r is the relative path, and rv is json.Unmarshalled to
func (sc *simpleClient) Get(r string, rv interface{}) error {
	if !sc.Quiet {
		log.Printf("GET %s%s", sc.API, r)
	}
	req, err := http.NewRequest(http.MethodGet, sc.API+r, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", sc.Authorization)
	resp, err := sc.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return errors.New("bad status code")
	}

	return json.NewDecoder(resp.Body).Decode(rv)
}

// List makes a GET request, to list resources, where we will follow the "next_url"
// to page results, and calls "f" as a callback to process each resource found
func (sc *simpleClient) List(r string, f func(*resource) error) error {
	for r != "" {
		var res struct {
			NextURL   string `json:"next_url"`
			Resources []*resource
		}
		err := sc.Get(r, &res)
		if err != nil {
			return err
		}

		for _, rr := range res.Resources {
			err = f(rr)
			if err != nil {
				return err
			}
		}

		r = res.NextURL
	}
	return nil
}

// resource captures fields that we care about when
// retrieving data from CloudFoundry
type resource struct {
	Metadata struct {
		GUID      string    `json:"guid"`       // app
		UpdatedAt time.Time `json:"updated_at"` // buildpack
		URL       string    `json:"url"`        // app
	} `json:"metadata"`
	Entity struct {
		Name               string    // org, space
		SpacesURL          string    `json:"spaces_url"`              // org
		UsersURL           string    `json:"users_url"`               // org
		ManagersURL        string    `json:"managers_url"`            // org, space
		BillingManagersURL string    `json:"billing_managers_url"`    // org
		AuditorsURL        string    `json:"auditors_url"`            // org, space
		DevelopersURL      string    `json:"developers_url"`          // space
		AppsURL            string    `json:"apps_url"`                // space
		BuildpackGUID      string    `json:"detected_buildpack_guid"` // app
		Buildpack          string    `json:"buildpack"`               // app
		Admin              bool      // user
		Username           string    // user
		Filename           string    `json:"filename"`           // buildpack
		Enabled            bool      `json:"enabled"`            // buildpack
		PackageUpdatedAt   time.Time `json:"package_updated_at"` // app
		Memory             int       `json:"memory"`             // app in gb?
		Instances          int       `json:"instances"`          // app
		DiskQuota          int       `json:"disk_quota"`         // app in gb?
		State              string    `json:"state"`
	} `json:"entity"`
}

type reportMemoryUsage struct{}

func newSimpleClient(cliConnection plugin.CliConnection, quiet bool) (*simpleClient, error) {
	at, err := cliConnection.AccessToken()
	if err != nil {
		return nil, err
	}

	api, err := cliConnection.ApiEndpoint()
	if err != nil {
		return nil, err
	}

	skipSSL, err := cliConnection.IsSSLDisabled()
	if err != nil {
		return nil, err
	}

	httpClient := http.DefaultClient
	if skipSSL {
		if !quiet {
			log.Println("warning: skipping TLS validation...")
		}

		httpClient = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
		}
	}

	return &simpleClient{
		API:           api,
		Authorization: at,
		Quiet:         quiet,
		Client:        httpClient,
	}, nil
}

func (c *reportMemoryUsage) Run(cliConnection plugin.CliConnection, args []string) {
	outputJSON := false
	quiet := false

	fs := flag.NewFlagSet("report-memory-usage", flag.ExitOnError)
	fs.BoolVar(&outputJSON, "output-json", false, "if set sends JSON to stdout instead of a rendered table")
	fs.BoolVar(&quiet, "quiet", false, "if set suppressing printing of progress messages to stderr")
	err := fs.Parse(args[1:])
	if err != nil {
		log.Fatal(err)
	}

	client, err := newSimpleClient(cliConnection, quiet)
	if err != nil {
		log.Fatal(err)
	}

	switch args[0] {
	case "report-memory-usage":
		err := c.reportMemoryUsage(client, os.Stdout, outputJSON)
		if err != nil {
			log.Fatal(err)
		}
	}
}

type appUsageInfo struct {
	Key         string
	MemoryUsage int
	MemoryQuota int
}

type appStats map[string]*struct {
	Stats struct {
		DiskQuota int `json:"disk_quota"`
		MemQuota  int `json:"mem_quota"`
		Usage     struct {
			Disk int `json:"disk"`
			Mem  int `json:"mem"`
		} `json:"usage"`
	} `json:"stats"`
}

func noSlash(s string) string {
	return strings.Replace(s, "/", "-", -1)
}

func (c *reportMemoryUsage) reportMemoryUsage(client *simpleClient, out io.Writer, outputJSON bool) error {
	buildpacks := make(map[string]*resource)
	err := client.List("/v2/buildpacks", func(bp *resource) error {
		if bp.Entity.Enabled {
			buildpacks[bp.Entity.Name] = bp
		}
		return nil
	})
	if err != nil {
		return err
	}

	var allInfo []*appUsageInfo
	err = client.List("/v2/organizations", func(org *resource) error {
		return client.List(org.Entity.SpacesURL, func(space *resource) error {
			return client.List(space.Entity.AppsURL, func(app *resource) error {
				if app.Entity.State == "STOPPED" {
					return nil
				}
				var stats appStats
				err := client.Get(app.Metadata.URL+"/stats", &stats)
				if err != nil {
					return err
				}
				for instanceIdx, instanceStat := range stats {
					allInfo = append(allInfo, &appUsageInfo{
						Key: fmt.Sprintf("%s/%s/%s/%s",
							noSlash(org.Entity.Name),
							noSlash(space.Entity.Name),
							noSlash(app.Entity.Name),
							noSlash(instanceIdx),
						),
						MemoryUsage: instanceStat.Stats.Usage.Mem,
						MemoryQuota: instanceStat.Stats.MemQuota,
					})
				}
				return nil
			})
		})
	})
	if err != nil {
		return err
	}

	totalQuota, totalUsage := make(map[string]int), make(map[string]int)
	for _, info := range allInfo {
		bits := strings.Split(info.Key, "/")
		for i := range bits {
			key := strings.Join(bits[:i], "/")
			totalQuota[key], totalUsage[key] = totalQuota[key]+info.MemoryQuota, totalUsage[key]+info.MemoryUsage
		}
	}
	for k, quota := range totalQuota {
		allInfo = append(allInfo, &appUsageInfo{
			Key:         k,
			MemoryUsage: totalUsage[k],
			MemoryQuota: quota,
		})
	}

	if outputJSON {
		return json.NewEncoder(out).Encode(allInfo)
	}

	sort.Sort(sort.Reverse(byTotalDisk(allInfo)))

	table := tablewriter.NewWriter(out)
	table.SetHeader([]string{"Key", "Usage", "Quota", "Percent"})
	for _, row := range allInfo {
		table.Append([]string{
			fmt.Sprintf("/%s", row.Key),
			toHumanSize(row.MemoryUsage),
			toHumanSize(row.MemoryQuota),
			toPercent(row.MemoryUsage, row.MemoryQuota),
		})
	}
	table.Render()

	return nil
}

func toPercent(num, denom int) string {
	if denom == 0 {
		return "NaN"
	}
	return fmt.Sprintf("%d%%", (num*100.0)/denom)
}

func toHumanSize(b int) string {
	units := []string{"B", "KB", "MB", "GB", "TB", "PB", "EB"}
	for _, u := range units[:len(units)-1] {
		if b < 1024 {
			return fmt.Sprintf("%d %s", b, u)
		}
		b /= 1024
	}
	return fmt.Sprintf("%d %s", b, units[len(units)-1])
}

type byTotalDisk []*appUsageInfo

func (b byTotalDisk) Len() int {
	return len(b)
}

func (b byTotalDisk) Less(i, j int) bool {
	return (b[i].MemoryQuota) < (b[j].MemoryQuota)
}

func (b byTotalDisk) Swap(i, j int) {
	b[i], b[j] = b[j], b[i]
}

func (c *reportMemoryUsage) GetMetadata() plugin.PluginMetadata {
	return plugin.PluginMetadata{
		Name: "report-memory-usage",
		Version: plugin.VersionType{
			Major: 0,
			Minor: 2,
			Build: 0,
		},
		MinCliVersion: plugin.VersionType{
			Major: 6,
			Minor: 7,
			Build: 0,
		},
		Commands: []plugin.Command{
			{
				Name:     "report-memory-usage",
				HelpText: "Report all buildpacks used in installation",
				UsageDetails: plugin.Usage{
					Usage: "cf report-memory-usage",
					Options: map[string]string{
						"output-json": "if set sends JSON to stdout instead of a rendered table",
						"quiet":       "if set suppresses printing of progress messages to stderr",
					},
				},
			},
		},
	}
}

func main() {
	plugin.Start(&reportMemoryUsage{})
}
