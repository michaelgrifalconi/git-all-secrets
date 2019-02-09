package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
)

func gitclone(cloneURL string, repoName string, wg *sync.WaitGroup) {
	defer wg.Done()

	cmd := exec.Command("/usr/bin/git", "clone", cloneURL, repoName)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		fmt.Println(fmt.Sprint(err) + ": " + stderr.String())
		// panic(err)
	}
}

func gitRepoURL(path string) (string, error) {
	out, err := exec.Command("/usr/bin/git", "-C", path, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return "", err
	}
	url := strings.TrimSuffix(string(out), "\n")
	return url, nil
}

// Moving cloning logic out of individual functions
func executeclone(repo *github.Repository, directory string, wg *sync.WaitGroup) {
	urlToClone := ""

	switch *scanPrivateReposOnly {
	case false:
		urlToClone = *repo.CloneURL
	case true:
		urlToClone = *repo.SSHURL
	default:
		urlToClone = *repo.CloneURL
	}

	if *enterpriseURL != "" {
		urlToClone = *repo.SSHURL
	}

	var orgclone sync.WaitGroup
	if !*cloneForks && *repo.Fork {
		fmt.Println(*repo.Name + " is a fork and the cloneFork flag was set to false so moving on..")
	} else {
		// clone it
		orgclone.Add(1)
		fmt.Println(urlToClone)
		func(orgclone *sync.WaitGroup, urlToClone string, directory string) {
			enqueueJob(func() {
				gitclone(urlToClone, directory, orgclone)
			})
		}(&orgclone, urlToClone, directory)
	}

	orgclone.Wait()
	wg.Done()
}

func cloneorgrepos(ctx context.Context, client *github.Client, org string) error {

	Info("Cloning the repositories of the organization: " + org)
	Info("If the token provided belongs to a user in this organization, this will also clone all public AND private repositories of this org, irrespecitve of the scanPrivateReposOnly flag being set..")

	var orgRepos []*github.Repository
	opt := &github.RepositoryListByOrgOptions{
		ListOptions: github.ListOptions{PerPage: 10},
	}

	for {
		repos, resp, err := client.Repositories.ListByOrg(ctx, org, opt)
		check(err)
		orgRepos = append(orgRepos, repos...) //adding to the repo array
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	var orgrepowg sync.WaitGroup

	//iterating through the repo array
	for _, repo := range orgRepos {
		if strings.Contains(*blacklist, *repo.Name) {
			fmt.Println("Repo " + *repo.Name + " is in the repo blacklist, moving on..")
		} else {
			orgrepowg.Add(1)
			go executeclone(repo, "/tmp/repos/org/"+org+"/"+*repo.Name, &orgrepowg)
		}
	}

	orgrepowg.Wait()
	fmt.Println("Done cloning org repos.")
	return nil
}

func cloneuserrepos(ctx context.Context, client *github.Client, user string) error {
	Info("Cloning " + user + "'s repositories")
	Info("If the scanPrivateReposOnly flag is set, this will only scan the private repositories of this user. If that flag is not set, only public repositories are scanned. ")

	var uname string
	var userRepos []*github.Repository
	var opt3 *github.RepositoryListOptions

	if *scanPrivateReposOnly {
		uname = ""
		opt3 = &github.RepositoryListOptions{
			Visibility:  "private",
			ListOptions: github.ListOptions{PerPage: 10},
		}
	} else {
		uname = user
		opt3 = &github.RepositoryListOptions{
			ListOptions: github.ListOptions{PerPage: 10},
		}
	}

	for {
		uRepos, resp, err := client.Repositories.List(ctx, uname, opt3)
		check(err)
		userRepos = append(userRepos, uRepos...) //adding to the userRepos array
		if resp.NextPage == 0 {
			break
		}
		opt3.Page = resp.NextPage
	}

	var userrepowg sync.WaitGroup
	//iterating through the userRepos array
	for _, userRepo := range userRepos {
		userrepowg.Add(1)
		go executeclone(userRepo, "/tmp/repos/users/"+user+"/"+*userRepo.Name, &userrepowg)
	}

	userrepowg.Wait()
	fmt.Println("Done cloning user repos.")
	return nil
}

func cloneusergists(ctx context.Context, client *github.Client, user string) error {
	Info("Cloning " + user + "'s gists")
	Info("Irrespective of the scanPrivateReposOnly flag being set or not, this will scan all public AND secret gists of a user whose token is provided")

	var gisturl string

	var userGists []*github.Gist
	opt4 := &github.GistListOptions{
		ListOptions: github.ListOptions{PerPage: 10},
	}
	for {
		uGists, resp, err := client.Gists.List(ctx, user, opt4)
		check(err)
		userGists = append(userGists, uGists...)
		if resp.NextPage == 0 {
			break
		}
		opt4.Page = resp.NextPage
	}

	var usergistclone sync.WaitGroup
	//iterating through the userGists array
	for _, userGist := range userGists {
		usergistclone.Add(1)

		if *enterpriseURL != "" {
			d := strings.Split(*userGist.GitPullURL, "/")[2]
			f := strings.Split(*userGist.GitPullURL, "/")[4]
			gisturl = "git@" + d + ":gist/" + f
		} else {
			gisturl = *userGist.GitPullURL
		}

		fmt.Println(gisturl)

		//cloning the individual user gists
		func(gisturl string, userGist *github.Gist, user string, usergistclone *sync.WaitGroup) {
			enqueueJob(func() {
				gitclone(gisturl, "/tmp/repos/users/"+user+"/"+*userGist.ID, usergistclone)
			})
		}(gisturl, userGist, user, &usergistclone)
	}

	usergistclone.Wait()
	return nil
}

func listallusers(ctx context.Context, client *github.Client, org string) ([]*github.User, error) {
	Info("Listing users of the organization and their repositories and gists")
	var allUsers []*github.User
	opt2 := &github.ListMembersOptions{
		ListOptions: github.ListOptions{PerPage: 10},
	}

	for {
		users, resp, err := client.Organizations.ListMembers(ctx, org, opt2)
		check(err)
		allUsers = append(allUsers, users...) //adding to the allUsers array
		if resp.NextPage == 0 {
			break
		}
		opt2.Page = resp.NextPage
	}

	return allUsers, nil
}

func cloneTeamRepos(ctx context.Context, client *github.Client, org string, teamName string) error {

	// var team *github.Team
	team, err := findTeamByName(ctx, client, org, teamName)

	if team != nil {
		Info("Cloning the repositories of the team: " + *team.Name + "(" + strconv.FormatInt(*team.ID, 10) + ")")
		var teamRepos []*github.Repository
		listTeamRepoOpts := &github.ListOptions{
			PerPage: 10,
		}

		Info("Listing team repositories...")
		for {
			repos, resp, err := client.Organizations.ListTeamRepos(ctx, *team.ID, listTeamRepoOpts)
			check(err)
			teamRepos = append(teamRepos, repos...) //adding to the repo array
			if resp.NextPage == 0 {
				break
			}
			listTeamRepoOpts.Page = resp.NextPage
		}

		var teamrepowg sync.WaitGroup

		//iterating through the repo array
		for _, repo := range teamRepos {
			teamrepowg.Add(1)
			go executeclone(repo, "/tmp/repos/team/"+*repo.Name, &teamrepowg)
		}

		teamrepowg.Wait()

	} else {
		fmt.Println("Unable to find the team '" + teamName + "'; perhaps the user is not a member?\n")
		if err != nil {
			fmt.Println("Error was:")
			fmt.Println(err)
		}
		os.Exit(2)
	}
	return nil
}

func authenticatetogit(ctx context.Context, token string) (*github.Client, error) {
	var client *github.Client
	var err error

	//Authenticating to Github using the token
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)

	if *enterpriseURL == "" {
		client = github.NewClient(tc)
	} else if *enterpriseURL != "" {
		client, err = github.NewEnterpriseClient(*enterpriseURL, *enterpriseURL, tc)
		if err != nil {
			fmt.Printf("NewEnterpriseClient returned unexpected error: %v", err)
		}
	}
	return client, nil
}

func findTeamByName(ctx context.Context, client *github.Client, org string, teamName string) (*github.Team, error) {

	listTeamsOpts := &github.ListOptions{
		PerPage: 10,
	}
	Info("Listing teams...")
	for {
		teams, resp, err := client.Organizations.ListTeams(ctx, org, listTeamsOpts)
		check(err)
		//check the name here--try to avoid additional API calls if we've found the team
		for _, team := range teams {
			if *team.Name == teamName {
				return team, nil
			}
		}
		if resp.NextPage == 0 {
			break
		}
		listTeamsOpts.Page = resp.NextPage
	}
	return nil, nil
}
