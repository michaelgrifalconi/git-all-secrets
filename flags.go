package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/google/go-github/github"
)

var (
	org                  = flag.String("org", "", "Name of the Organization to scan. Example: secretorg123")
	token                = flag.String("token", "", "Github Personal Access Token. This is required.")
	outputFile           = flag.String("output", "results.txt", "Output file to save the results.")
	user                 = flag.String("user", "", "Name of the Github user to scan. Example: secretuser1")
	repoURL              = flag.String("repoURL", "", "HTTPS URL of the Github repo to scan. Example: https://github.com/anshumantestorg/repo1.git")
	gistURL              = flag.String("gistURL", "", "HTTPS URL of the Github gist to scan. Example: https://gist.github.com/secretuser1/81963f276280d484767f9be895316afc")
	cloneForks           = flag.Bool("cloneForks", false, "Option to clone org and user repos that are forks. Default is false")
	orgOnly              = flag.Bool("orgOnly", false, "Option to skip cloning user repo's when scanning an org. Default is false")
	toolName             = flag.String("toolName", "all", "Specify whether to run thog or repo-supervisor")
	teamName             = flag.String("teamName", "", "Name of the Organization Team which has access to private repositories for scanning.")
	scanPrivateReposOnly = flag.Bool("scanPrivateReposOnly", false, "Option to scan private repositories only. Default is false")
	enterpriseURL        = flag.String("enterpriseURL", "", "Base URL of the Github Enterprise")
	threads              = flag.Int("threads", 10, "Amount of parallel threads")
	thogEntropy          = flag.Bool("thogEntropy", false, "Option to include high entropy secrets when truffleHog is used")
	mergeOutput          = flag.Bool("mergeOutput", false, "Merge the output files of all the tools used into one JSON file")
	blacklist            = flag.String("blacklist", "", "Comma seperated values of Repos to Skip Scanning for")
	executionQueue       chan bool
	scanOnly             = flag.Bool("scanOnly", false, "Just scan, do not download. Please make sure to mount a volume with correct file structure.") //TODO improve docs about this
)

func stringInSlice(a string, list []*github.Repository) (bool, error) {
	for _, b := range list {
		if *b.SSHURL == a || *b.CloneURL == a {
			return true, nil
		}
	}
	return false, nil
}

func checkifsshkeyexists() error {
	fmt.Println("Checking to see if the SSH key exists or not..")

	fi, err := os.Stat("/root/.ssh/id_rsa")
	if err == nil && fi.Size() > 0 {
		fmt.Println("SSH key exists and file size > 0 so continuing..")
	}
	if err != nil {
		fmt.Println(err)
		os.Exit(2)
	}
	return nil
}

func checkflags(token string, org string, user string, repoURL string, gistURL string, teamName string, scanPrivateReposOnly bool, orgOnly bool, toolName string, enterpriseURL string, thogEntropy bool) error {
	if token == "" {
		fmt.Println("Need a Github personal access token. Please provide that using the -token flag")
		os.Exit(2)
	} else if org == "" && user == "" && repoURL == "" && gistURL == "" {
		fmt.Println("org, user, repoURL and gistURL can't all be empty. Please provide just one of these values")
		os.Exit(2)
	} else if org != "" && (user != "" || repoURL != "" || gistURL != "") {
		fmt.Println("Can't have org along with any of user, repoURL or gistURL. Please provide just one of these values")
		os.Exit(2)
	} else if user != "" && (org != "" || repoURL != "" || gistURL != "") {
		fmt.Println("Can't have user along with any of org, repoURL or gistURL. Please provide just one of these values")
		os.Exit(2)
	} else if repoURL != "" && (org != "" || user != "" || gistURL != "") {
		fmt.Println("Can't have repoURL along with any of org, user or gistURL. Please provide just one of these values")
		os.Exit(2)
	} else if gistURL != "" && (org != "" || repoURL != "" || user != "") {
		fmt.Println("Can't have gistURL along with any of org, user or repoURL. Please provide just one of these values")
		os.Exit(2)
	} else if thogEntropy && !(toolName == "all" || toolName == "thog") {
		fmt.Println("thogEntropy flag should be used only when thog is being run. So, either leave the toolName blank or the toolName should be thog")
		os.Exit(2)
	} else if enterpriseURL == "" && (repoURL != "" || gistURL != "") {
		var ed, url string

		if repoURL != "" {
			url = repoURL
		} else if gistURL != "" {
			url = gistURL
		}

		if strings.Split(strings.Split(url, ":")[0], "@")[0] == "git" {
			fmt.Println("SSH URL")
			ed = strings.Split(strings.Split(url, ":")[0], "@")[1]
		} else if strings.Split(url, "/")[0] == "https:" {
			fmt.Println("HTTPS URL")
			ed = strings.Split(url, "/")[2]
		}

		matched, err := regexp.MatchString("github.com", ed)
		check(err)

		if !matched {
			fmt.Println("By the domain provided in the repoURL/gistURL, it looks like you are trying to scan a Github Enterprise repo/gist. Therefore, you need to provide the enterpriseURL flag as well")
			os.Exit(2)
		}
	} else if teamName != "" && org == "" {
		fmt.Println("Can't have a teamName without an org! Please provide a value for org along with the team name")
		os.Exit(2)
	} else if orgOnly && org == "" {
		fmt.Println("orgOnly flag should be used with a valid org")
		os.Exit(2)
	} else if scanPrivateReposOnly && user == "" && repoURL == "" && org == "" {
		fmt.Println("scanPrivateReposOnly flag should be used along with either the user, org or the repoURL")
		os.Exit(2)
	} else if scanPrivateReposOnly && (user != "" || repoURL != "" || org != "") {
		fmt.Println("scanPrivateReposOnly flag is provided with either the user, the repoURL or the org")

		err := checkifsshkeyexists()
		check(err)

		//Authenticating to Github using the token
		ctx1 := context.Background()
		client1, err := authenticatetogit(ctx1, token)
		check(err)

		if user != "" || repoURL != "" {
			var userRepos []*github.Repository
			opt3 := &github.RepositoryListOptions{
				Affiliation: "owner",
				ListOptions: github.ListOptions{PerPage: 10},
			}

			for {
				uRepos, resp, err := client1.Repositories.List(ctx1, "", opt3)
				check(err)
				userRepos = append(userRepos, uRepos...) //adding to the userRepos array
				if resp.NextPage == 0 {
					break
				}
				opt3.Page = resp.NextPage
			}

			if user != "" {
				fmt.Println("scanPrivateReposOnly flag is provided along with the user")
				fmt.Println("Checking to see if the token provided belongs to the user or not..")

				if *userRepos[0].Owner.Login == user {
					fmt.Println("Token belongs to the user")
				} else {
					fmt.Println("Token does not belong to the user. Please provide the correct token for the user mentioned.")
					os.Exit(2)
				}

			} else if repoURL != "" {
				fmt.Println("scanPrivateReposOnly flag is provided along with the repoURL")
				fmt.Println("Checking to see if the repo provided belongs to the user or not..")
				val, err := stringInSlice(repoURL, userRepos)
				check(err)
				if val {
					fmt.Println("Repo belongs to the user provided")
				} else {
					fmt.Println("Repo does not belong to the user whose token is provided. Please provide a valid repoURL that belongs to the user whose token is provided.")
					os.Exit(2)
				}
			}
		} else if org != "" && teamName == "" {
			var orgRepos []*github.Repository

			opt3 := &github.RepositoryListByOrgOptions{
				Type:        "private",
				ListOptions: github.ListOptions{PerPage: 10},
			}

			for {
				repos, resp, err := client1.Repositories.ListByOrg(ctx1, org, opt3)
				check(err)
				orgRepos = append(orgRepos, repos...)
				if resp.NextPage == 0 {
					break
				}
				opt3.Page = resp.NextPage
			}

			fmt.Println("scanPrivateReposOnly flag is provided along with the org")
			fmt.Println("Checking to see if the token provided belongs to a user in the org or not..")

			var i int
			if i >= 0 && i < len(orgRepos) {
				fmt.Println("Private Repos exist in this org and token belongs to a user in this org")
			} else {
				fmt.Println("Even though the token belongs to a user in this org, there are no Private repos in this org")
				os.Exit(2)
			}

		}

	} else if scanPrivateReposOnly && gistURL != "" {
		fmt.Println("scanPrivateReposOnly flag should NOT be provided with the gistURL since its a private repository or multiple private repositories that we are looking to scan. Please provide either a user, an org or a private repoURL")
		os.Exit(2)
	} else if !(toolName == "thog" || toolName == "repo-supervisor" || toolName == "all") {
		fmt.Println("Please enter either thog or repo-supervisor. Default is all.")
		os.Exit(2)
	} else if repoURL != "" && !scanPrivateReposOnly && enterpriseURL == "" {
		if strings.Split(repoURL, "@")[0] == "git" {
			fmt.Println("Since the repoURL is a SSH URL and no enterprise URL is provided, it is required to have the scanPrivateReposOnly flag and the SSH key mounted on a volume")
			os.Exit(2)
		}
	} else if enterpriseURL != "" {
		fmt.Println("Since enterpriseURL is provided, checking to see if the SSH key is also mounted or not")

		err := checkifsshkeyexists()
		check(err)
	}

	return nil
}
