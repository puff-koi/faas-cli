// Copyright (c) OpenFaaS Author(s) 2017. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var (
	scope         string
	authURL       string
	clientID      string
	audience      string
	listenPort    int
	launchBrowser bool
)

func init() {
	authCmd.Flags().StringVarP(&gateway, "gateway", "g", defaultGateway, "Gateway URL starting with http(s)://")
	authCmd.Flags().StringVar(&authURL, "auth-url", "", "OAuth2 Authorize URL i.e. http://idp/oauth/authorize")
	authCmd.Flags().StringVar(&clientID, "client-id", "", "OAuth2 client_id")
	authCmd.Flags().IntVar(&listenPort, "listen-port", 31111, "OAuth2 local port for receiving cookie")
	authCmd.Flags().StringVar(&audience, "audience", "", "OAuth2 audience")
	authCmd.Flags().BoolVar(&launchBrowser, "launch-browser", true, "Launch browser for OAuth2 redirect")

	faasCmd.AddCommand(authCmd)
}

var authCmd = &cobra.Command{
	Use:     `auth [--auth-url AUTH_URL | --client-id CLIENT_ID | --audience AUDIENCE | --scope SCOPE | --launch-browser LAUNCH_BROWSER]`,
	Short:   "Obtain a token for your OpenFaaS gateway",
	Long:    "Authenticate to an OpenFaaS gateway using OAuth2.",
	Example: `faas-cli auth --client-id my-id --auth-url https://auth0.com/authorize --scope "oidc profile" --audience my-id`,
	RunE:    runAuth,
	PreRunE: preRunAuth,
}

func preRunAuth(cmd *cobra.Command, args []string) error {
	return checkValues(authURL,
		clientID,
	)
}

func checkValues(authURL, clientID string) error {

	if len(authURL) == 0 {
		return fmt.Errorf("--auth-url is required and must be a valid OIDC /authorize URL")
	}

	u, uErr := url.Parse(authURL)
	if uErr != nil {
		return fmt.Errorf("--auth-url is an invalid URL: %s", uErr.Error())
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("--auth-url is an invalid URL: %s", u.String())
	}

	if len(clientID) == 0 {
		return fmt.Errorf("--client-id is required")
	}

	return nil
}

func runAuth(cmd *cobra.Command, args []string) error {
	context, cancel := context.WithCancel(context.TODO())
	defer cancel()

	server := &http.Server{
		Addr:           fmt.Sprintf(":%d", listenPort),
		ReadTimeout:    5 * time.Second,
		WriteTimeout:   5 * time.Second,
		MaxHeaderBytes: 1 << 20, // Max header of 1MB
		Handler:        http.HandlerFunc(makeCallbackHandler(cancel)),
	}

	go func() {
		fmt.Printf("Starting local token server on port %d\n", listenPort)
		if err := server.ListenAndServe(); err != nil {
			panic(err)
		}

		select {
		case <-context.Done():
			break
		}
	}()

	defer server.Shutdown(context)

	q := url.Values{}
	q.Add("client_id", clientID)

	q.Add("state", fmt.Sprintf("%d", time.Now().UnixNano()))
	q.Add("nonce", fmt.Sprintf("%d", time.Now().UnixNano()))
	q.Add("response_type", "token")
	q.Add("scope", scope)
	q.Add("audience", audience)

	q.Add("redirect_uri", fmt.Sprintf("%s/oauth/callback", fmt.Sprintf("http://127.0.0.1:%d", listenPort)))
	authURLVal, _ := url.Parse(authURL)
	authURLVal.RawQuery = q.Encode()

	browserBase := authURLVal

	fmt.Printf("Launching browser: %s\n", browserBase)
	if launchBrowser {
		err := launchURL(browserBase.String())
		if err != nil {
			return errors.Wrap(err, "unable to launch browser")
		}
	}

	<-context.Done()

	return nil
}

// launchURL opens a URL with the default browser for Linux, MacOS or Windows.
func launchURL(serverURL string) error {
	ctx := context.Background()
	var command *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		command = exec.CommandContext(ctx, "sh", "-c", fmt.Sprintf(`xdg-open "%s"`, serverURL))
	case "darwin":
		command = exec.CommandContext(ctx, "sh", "-c", fmt.Sprintf(`open "%s"`, serverURL))
	case "windows":
		escaped := strings.Replace(serverURL, "&", "^&", -1)
		command = exec.CommandContext(ctx, "cmd", "/c", fmt.Sprintf(`start %s`, escaped))
	}
	command.Stdout = os.Stdout
	command.Stdin = os.Stdin
	command.Stderr = os.Stderr
	return command.Run()
}

func makeCallbackHandler(cancel context.CancelFunc) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {

		if v := r.URL.Query().Get("fragment"); len(v) > 0 {
			q, err := url.ParseQuery(v)
			if err != nil {
				panic(errors.Wrap(err, "unable to parse fragment response from browser redirect"))
			}

			if token := q.Get("access_token"); len(token) > 0 {
				fmt.Printf("Example:\n\t./faas-cli list --gateway \"%s\" --token \"%s\"\n", gateway, token)
			} else {
				fmt.Printf("Unable to detect a valid access_token in URL fragment. Check your credentials or contact your administrator.\n")
			}

			cancel()
			return
		}

		if r.Body != nil {
			defer r.Body.Close()
		}
		w.Write([]byte(buildCaptureFragment()))
	}
}

func buildCaptureFragment() string {
	return `
<html>
<head>
<title>OpenFaaS CLI Authorization flow</title>
<script>
	var xhttp = new XMLHttpRequest();
	xhttp.onreadystatechange = function() {
		if (this.readyState == 4 && this.status == 200) {
			console.log(xhttp.responseText)
		}
	};
	xhttp.open("GET", "/oauth2/callback?fragment="+document.location.hash.slice(1), true);
	xhttp.send();
</script>
</head>
<body>
 Authorization flow complete. Please close this browser window.
</body>
</html>`
}