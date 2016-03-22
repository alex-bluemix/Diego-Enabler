package main

import (
	"errors"
	"fmt"
	"os"

	"crypto/tls"
	"net/http"

	"github.com/cloudfoundry-incubator/diego-enabler/api"
	"github.com/cloudfoundry-incubator/diego-enabler/commands"
	"github.com/cloudfoundry-incubator/diego-enabler/diego_support"
	"github.com/cloudfoundry-incubator/diego-enabler/models"
	"github.com/cloudfoundry/cli/cf/terminal"
	"github.com/cloudfoundry/cli/cf/trace"
	"github.com/cloudfoundry/cli/plugin"
)

type DiegoEnabler struct{}

func (c *DiegoEnabler) GetMetadata() plugin.PluginMetadata {
	return plugin.PluginMetadata{
		Name: "Diego-Enabler",
		Version: plugin.VersionType{
			Major: 1,
			Minor: 0,
			Build: 1,
		},
		Commands: []plugin.Command{
			{
				Name:     "enable-diego",
				HelpText: "enable Diego support for an app",
				UsageDetails: plugin.Usage{
					Usage: "cf enable-diego APP_NAME",
				},
			},
			{
				Name:     "disable-diego",
				HelpText: "disable Diego support for an app",
				UsageDetails: plugin.Usage{
					Usage: "cf disable-diego APP_NAME",
				},
			},
			{
				Name:     "has-diego-enabled",
				HelpText: "Check if Diego support is enabled for an app",
				UsageDetails: plugin.Usage{
					Usage: "cf has-diego-enabled APP_NAME",
				},
			},
			{
				Name:     "diego-apps",
				HelpText: "Lists all apps running on the Diego runtime that are visible to the user",
				UsageDetails: plugin.Usage{
					Usage: "cf diego-apps",
				},
			},
			{
				Name:     "dea-apps",
				HelpText: "Lists all apps running on the DEA runtime that are visible to the user",
				UsageDetails: plugin.Usage{
					Usage: "cf dea-apps",
				},
			},
		},
	}
}

func main() {
	plugin.Start(new(DiegoEnabler))
}

func (c *DiegoEnabler) Run(cliConnection plugin.CliConnection, args []string) {
	if args[0] == "enable-diego" && len(args) == 2 {
		c.toggleDiegoSupport(true, cliConnection, args[1])
	} else if args[0] == "disable-diego" && len(args) == 2 {
		c.toggleDiegoSupport(false, cliConnection, args[1])
	} else if args[0] == "has-diego-enabled" && len(args) == 2 {
		c.isDiegoEnabled(cliConnection, args[1])
	} else if args[0] == "diego-apps" && len(args) == 1 {
		c.showApps(cliConnection, commands.DiegoApps)
	} else if args[0] == "dea-apps" && len(args) == 1 {
		c.showApps(cliConnection, commands.DeaApps)
	} else {
		c.showUsage(args)
	}
}

func (c *DiegoEnabler) showApps(cliConnection plugin.CliConnection, appsGetter func(commands.RequestFactory, commands.CloudControllerClient, commands.ApplicationsParser, commands.PaginatedParser) (models.Applications, error)) {
	username, err := cliConnection.Username()
	if err != nil {
		exitWithError(err, []string{})
	}

	if err := verifyLoggedIn(cliConnection); err != nil {
		exitWithError(err, []string{})
	}

	accessToken, err := cliConnection.AccessToken()
	if err != nil {
		exitWithError(err, []string{})
	}

	fmt.Printf("Getting apps on the Diego runtime as %s...\n", terminal.EntityNameColor(username))

	pageParser := api.PageParser{}
	appsParser := models.ApplicationsParser{}
	spacesParser := models.SpacesParser{}

	apiEndpoint, err := cliConnection.ApiEndpoint()
	if err != nil {
		exitWithError(err, []string{})
	}

	apiClient, err := api.NewApiClient(apiEndpoint, accessToken)
	if err != nil {
		exitWithError(err, []string{})
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	appRequestFactory := apiClient.HandleFiltersAndParameters(
		apiClient.Authorize(apiClient.NewGetAppsRequest),
	)

	apps, err := appsGetter(appRequestFactory, httpClient, appsParser, pageParser)
	if err != nil {
		exitWithError(err, []string{})
	}

	spaceRequestFactory := apiClient.HandleFiltersAndParameters(
		apiClient.Authorize(apiClient.NewGetSpacesRequest),
	)

	spaces, err := commands.Spaces(spaceRequestFactory, httpClient, spacesParser, pageParser)
	if err != nil {
		exitWithError(err, []string{})
	}

	spaceMap := make(map[string]models.Space)
	for _, space := range spaces {
		spaceMap[space.Guid] = space
	}

	sayOk()

	traceEnv := os.Getenv("CF_TRACE")
	traceLogger := trace.NewLogger(false, traceEnv, "")
	ui := terminal.NewUI(os.Stdin, terminal.NewTeePrinter(), traceLogger)

	headers := []string{
		"name",
		"space",
		"org",
	}
	t := terminal.NewTable(ui, headers)

	for _, app := range apps {
		t.Add(app.Name, spaceDisplayFor(app, spaceMap), orgDisplayFor(app, spaceMap))
	}

	t.Print()
}



func spaceDisplayFor(app models.Application, spaces map[string]models.Space) string {
	var display string

	if len(spaces) == 0 {
		display = app.SpaceGuid
	} else {
		space, ok := spaces[app.SpaceGuid]
		if ok {
			display = space.Name
		} else {
			display = app.SpaceGuid
		}
	}

	return display
}

func orgDisplayFor(app models.Application, spaces map[string]models.Space) string {
	if len(spaces) == 0 {
		return ""
	}

	space, ok := spaces[app.SpaceGuid]
	if !ok {
		return ""
	}

	if space.Organization.Name != "" {
		return space.Organization.Name
	}

	return space.OrganizationGuid
}

func (c *DiegoEnabler) showUsage(args []string) {
	for _, cmd := range c.GetMetadata().Commands {
		if cmd.Name == args[0] {
			fmt.Println("Invalid Usage: \n", cmd.UsageDetails.Usage)
		}
	}
}

func (c *DiegoEnabler) toggleDiegoSupport(on bool, cliConnection plugin.CliConnection, appName string) {
	d := diego_support.NewDiegoSupport(cliConnection)

	fmt.Printf("Setting %s Diego support to %t\n", appName, on)
	app, err := cliConnection.GetApp(appName)
	if err != nil {
		exitWithError(err, []string{})
	}

	if output, err := d.SetDiegoFlag(app.Guid, on); err != nil {
		fmt.Println("err 1", err, output)
		exitWithError(err, output)
	}
	sayOk()

	fmt.Printf("Verifying %s Diego support is set to %t\n", appName, on)
	app, err = cliConnection.GetApp(appName)
	if err != nil {
		exitWithError(err, []string{})
	}

	if app.Diego == on {
		sayOk()
	} else {
		sayFailed()
		fmt.Printf("Diego support for %s is NOT set to %t\n\n", appName, on)
		os.Exit(1)
	}
}

func (c *DiegoEnabler) isDiegoEnabled(cliConnection plugin.CliConnection, appName string) {
	app, err := cliConnection.GetApp(appName)
	if err != nil {
		exitWithError(err, []string{})
	}

	if app.Guid == "" {
		sayFailed()
		fmt.Printf("App %s not found\n\n", appName)
		os.Exit(1)
	}

	fmt.Println(app.Diego)
}

func exitWithError(err error, output []string) {
	sayFailed()
	fmt.Println("Error: ", err)
	for _, str := range output {
		fmt.Println(str)
	}
	os.Exit(1)
}

func say(message string, color uint, bold int) string {
	return fmt.Sprintf("\033[%d;%dm%s\033[0m", bold, color, message)
}

func sayOk() {
	fmt.Println(say("Ok\n", 32, 1))
}

func sayFailed() {
	fmt.Println(say("FAILED", 31, 1))
}

func verifyLoggedIn(cliCon plugin.CliConnection) error {
	var result error

	if connected, err := cliCon.IsLoggedIn(); !connected {
		result = NotLoggedInError

		if err != nil {
			result = err
		}
	}

	return result
}

var NotLoggedInError = errors.New("You must be logged in")
