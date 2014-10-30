/*
Copyright 2014 Google Inc. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package main

import (
	"cups-connector/lib"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/golang/oauth2"
)

var (
	retainUserOAuthTokenFlag = flag.String("retain-user-oauth-token", "",
		"Whether to retain the user's OAuth token to enable automatic sharing")
	shareScopeFlag = flag.String("share-scope", "",
		"Scope (user, group, domain) to share printers with")
	proxyNameFlag       = flag.String("proxy-name", "", "GCP proxy name of this Connector")
	copyPrinterInfoFlag = flag.String("copy-printer-info-to-display-name", "",
		"Whether to copy the CUPS printer's printer-info attribute to the GCP printer's defaultDisplayName")
)

func getUserClient() (*http.Client, string, string) {
	options := oauth2.Options{
		ClientID:     lib.ClientID,
		ClientSecret: lib.ClientSecret,
		RedirectURL:  lib.RedirectURL,
		Scopes:       []string{lib.ScopeCloudPrint},
	}
	oauthConfig, err := oauth2.NewConfig(&options, lib.AuthURL, lib.TokenURL)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Login to Google as the user that will own the printers, then visit this URL:")
	fmt.Println("")
	fmt.Println(oauthConfig.AuthCodeURL("", "offline", "auto"))
	fmt.Println("")

	authCode := scanNonEmptyString("After authenticating, enter the provided code here: ")
	transport, err := oauthConfig.NewTransportWithCode(authCode)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("")
	fmt.Println("Acquired OAuth credentials for user account")

	var userRefreshToken, shareScope string
	if parsed, value := stringToBool(*retainUserOAuthTokenFlag); parsed {
		if value {
			userRefreshToken = transport.Token().RefreshToken
		} else {
			fmt.Println("The user account OAuth token will be thrown away.")
		}
	} else if scanYesOrNo("Would you like to retain the user OAuth token to enable automatic sharing? ") {
		userRefreshToken = transport.Token().RefreshToken
	} else {
		fmt.Println("The user account OAuth token will be thrown away.")
	}

	if len(userRefreshToken) > 0 {
		if len(*shareScopeFlag) > 0 {
			shareScope = *shareScopeFlag
		} else {
			shareScope = scanNonEmptyString("User or group email address, or domain name, to share with: ")
		}
	}

	return &http.Client{Transport: transport}, userRefreshToken, shareScope
}

func initRobotAccount(userClient *http.Client, proxy string) (string, string) {
	params := url.Values{}
	params.Set("oauth_client_id", lib.ClientID)
	params.Set("proxy", proxy)
	response, err := userClient.Get(lib.CreateRobotURL + "?" + params.Encode())
	if err != nil {
		log.Fatal(err)
	}
	if response.StatusCode != 200 {
		log.Fatal("failed to initialize robot account: " + response.Status)
	}

	var robotInit struct {
		Success  bool   `json:"success"`
		Message  string `json:"message"`
		XMPPJID  string `json:"xmpp_jid"`
		AuthCode string `json:"authorization_code"`
	}

	if err = json.NewDecoder(response.Body).Decode(&robotInit); err != nil {
		log.Fatal(err)
	}
	if !robotInit.Success {
		log.Fatal("failed to initialize robot account: " + robotInit.Message)
	}

	fmt.Println("Requested OAuth credentials for robot account")

	return robotInit.XMPPJID, robotInit.AuthCode
}

func verifyRobotAccount(authCode string) string {
	options := oauth2.Options{
		ClientID:     lib.ClientID,
		ClientSecret: lib.ClientSecret,
		RedirectURL:  lib.RedirectURL,
		Scopes:       []string{lib.ScopeCloudPrint, lib.ScopeGoogleTalk},
	}
	oauthConfig, err := oauth2.NewConfig(&options, lib.AuthURL, lib.TokenURL)
	if err != nil {
		log.Fatal(err)
	}

	token, err := oauthConfig.Exchange(authCode)
	if err != nil {
		log.Fatal(err)
	}

	return token.RefreshToken
}

func createRobotAccount(userClient *http.Client, proxy string) (string, string) {
	xmppJID, authCode := initRobotAccount(userClient, proxy)
	token := verifyRobotAccount(authCode)

	return xmppJID, token
}

func createConfigFile(xmppJID, robotRefreshToken, userRefreshToken, shareScope, proxy string, infoToDisplayName bool) {
	config := lib.Config{
		xmppJID,
		robotRefreshToken,
		userRefreshToken,
		shareScope,
		proxy,
		lib.DefaultGCPMaxConcurrentDownloads,
		lib.DefaultCUPSQueueSize,
		lib.DefaultCUPSPollIntervalPrinter,
		lib.DefaultCUPSPollIntervalJob,
		lib.DefaultPrinterAttributes,
		lib.DefaultCUPSJobFullUsername,
		infoToDisplayName,
		lib.DefaultSocketFilename,
	}

	if err := config.ToFile(); err != nil {
		log.Fatal(err)
	}
}

func scanNonEmptyString(prompt string) string {
	for {
		var answer string
		fmt.Printf(prompt)
		if length, err := fmt.Scan(&answer); err != nil {
			log.Fatal(err)
		} else if length > 0 {
			return answer
		}
	}
	panic("unreachable")
}

func scanYesOrNo(question string) bool {
	for {
		var answer string
		fmt.Printf(question)
		if _, err := fmt.Scan(&answer); err != nil {
			log.Fatal(err)
		} else if parsed, value := stringToBool(answer); parsed {
			return value
		}
	}
	panic("unreachable")
}

// The first return value is true if a boolean value could be parsed.
// The second return value is the parsed boolean value if the first return value is true.
func stringToBool(val string) (bool, bool) {
	if len(val) > 0 {
		switch strings.ToLower(val[0:1]) {
		case "y", "t", "1":
			return true, true
		case "n", "f", "0":
			return true, false
		default:
			return false, true
		}
	}
	return false, false
}

func getInfoToDisplayName() bool {
	if parsed, value := stringToBool(*copyPrinterInfoFlag); parsed {
		return value
	}
	return scanYesOrNo("Copy CUPS printer-info attribute to GCP defaultDisplayName? ")
}

func getProxy() string {
	if len(*proxyNameFlag) > 0 {
		return *proxyNameFlag
	}
	return scanNonEmptyString("Proxy name for this CloudPrint-CUPS server: ")
}

func main() {
	flag.Parse()

	userClient, userRefreshToken, shareScope := getUserClient()
	proxy := getProxy()
	infoToDisplayName := getInfoToDisplayName()
	fmt.Println("")

	xmppJID, robotRefreshToken := createRobotAccount(userClient, proxy)

	fmt.Println("Acquired OAuth credentials for robot account")
	fmt.Println("")

	createConfigFile(xmppJID, robotRefreshToken, userRefreshToken, shareScope, proxy, infoToDisplayName)

	fmt.Printf("The config file %s is ready to rock.\n", *lib.ConfigFilename)
	fmt.Println("Keep it somewhere safe, as it contains an OAuth token.")
}
