package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/google/go-github/github"
)

func enqueueJob(item func()) {
	executionQueue <- true
	go func() {
		item()
		<-executionQueue
	}()
}

// Info Function to show colored text
func Info(format string, args ...interface{}) {
	fmt.Printf("\x1b[34;1m%s\x1b[0m\n", fmt.Sprintf(format, args...))
}

func check(e error) {
	if e != nil {
		panic(e)
	} else if _, ok := e.(*github.RateLimitError); ok {
		log.Println("hit rate limit")
	} else if _, ok := e.(*github.AcceptedError); ok {
		log.Println("scheduled on GitHub side")
	}
}

func makeDirectories() error {
	os.MkdirAll("/tmp/repos/org", 0700)
	os.MkdirAll("/tmp/repos/team", 0700)
	os.MkdirAll("/tmp/repos/users", 0700)

	return nil
}

func main() {

	//Parsing the flags
	flag.Parse()

	executionQueue = make(chan bool, *threads)

	//Logic to check the program is ingesting proper flags
	err := checkflags(*token, *org, *user, *repoURL, *gistURL, *teamName, *scanPrivateReposOnly, *orgOnly, *toolName, *enterpriseURL, *thogEntropy)
	check(err)

	ctx := context.Background()

	//authN
	client, err := authenticatetogit(ctx, *token)
	check(err)

	//Creating some temp directories to store repos & results. These will be deleted in the end
	err = makeDirectories()
	check(err)

	//By now, we either have the org, user, repoURL or the gistURL. The program flow changes accordingly..

	if *org != "" { //If org was supplied
		if !*scanOnly {
			m := "Since org was provided, the tool will proceed to scan all the org repos, then all the user repos and user gists in a recursive manner"

			if *orgOnly {
				m = "Org was specified combined with orgOnly, the tool will proceed to scan only the org repos and nothing related to its users"
			}

			Info(m)

			//cloning all the repos of the org
			err := cloneorgrepos(ctx, client, *org)
			check(err)

			if *teamName != "" { //If team was supplied
				Info("Since team name was provided, the tool will clone all repos to which the team has access")

				//cloning all the repos of the team
				err := cloneTeamRepos(ctx, client, *org, *teamName)
				check(err)

			}

			//getting all the users of the org into the allUsers array
			allUsers, err := listallusers(ctx, client, *org)
			check(err)

			if !*orgOnly {

				//iterating through the allUsers array
				for _, user := range allUsers {

					//cloning all the repos of a user
					err1 := cloneuserrepos(ctx, client, *user.Login)
					check(err1)

					//cloning all the gists of a user
					err2 := cloneusergists(ctx, client, *user.Login)
					check(err2)

				}
			}
		}
		Info("Scanning all org repositories now..This may take a while so please be patient\n")
		err = scanorgrepos(*org)
		check(err)
		Info("Finished scanning all org repositories\n")

		if *teamName != "" { //If team was supplied
			Info("Scanning all team repositories now...This may take a while so please be patient\n")
			err = scanTeamRepos(*org)
			check(err)

			Info("Finished scanning all team repositories\n")
		}

		if !*orgOnly {

			Info("Scanning all user repositories and gists now..This may take a while so please be patient\n")
			var wguser sync.WaitGroup
			users, _ := ioutil.ReadDir("/tmp/repos/users/")
			for _, user := range users {
				wguser.Add(1)
				go scanforeachuser(user.Name(), &wguser)
			}
			wguser.Wait()
			Info("Finished scanning all user repositories and gists\n")
		}

	} else if *user != "" { //If user was supplied
		if !*scanOnly {
			Info("Since user was provided, the tool will proceed to scan all the user repos and user gists\n")
			err1 := cloneuserrepos(ctx, client, *user)
			check(err1)

			err2 := cloneusergists(ctx, client, *user)
			check(err2)
		}
		Info("Scanning all user repositories and gists now..This may take a while so please be patient\n")
		var wguseronly sync.WaitGroup
		wguseronly.Add(1)
		go scanforeachuser(*user, &wguseronly)
		wguseronly.Wait()
		Info("Finished scanning all user repositories and gists\n")

	} else if *repoURL != "" || *gistURL != "" { //If either repoURL or gistURL was supplied

		var url, repoorgist, fpath, rn, lastString, orgoruserName string
		var splitArray []string
		var bpath = "/tmp/repos/"

		if *repoURL != "" { //repoURL
			if *enterpriseURL != "" && strings.Split(strings.Split(*repoURL, "/")[0], "@")[0] != "git" {
				url = "git@" + strings.Split(*repoURL, "/")[2] + ":" + strings.Split(*repoURL, "/")[3] + "/" + strings.Split(*repoURL, "/")[4]
			} else {
				url = *repoURL
			}
			repoorgist = "repo"
		} else { //gistURL
			if *enterpriseURL != "" && strings.Split(strings.Split(*gistURL, "/")[0], "@")[0] != "git" {
				url = "git@" + strings.Split(*gistURL, "/")[2] + ":" + strings.Split(*gistURL, "/")[3] + "/" + strings.Split(*gistURL, "/")[4]
			} else {
				url = *gistURL
			}
			repoorgist = "gist"
		}

		Info("The tool will proceed to clone and scan: " + url + " only\n")

		if *enterpriseURL == "" && strings.Split(strings.Split(*gistURL, "/")[0], "@")[0] == "git" {
			splitArray = strings.Split(url, ":")
			lastString = splitArray[len(splitArray)-1]
		} else {
			splitArray = strings.Split(url, "/")
			lastString = splitArray[len(splitArray)-1]
		}

		if !*scanPrivateReposOnly {
			if *enterpriseURL != "" {
				orgoruserName = strings.Split(splitArray[0], ":")[1]
			} else {
				if *enterpriseURL == "" && strings.Split(strings.Split(*gistURL, "/")[0], "@")[0] == "git" {
					orgoruserName = splitArray[1]
				} else {
					orgoruserName = splitArray[3]
				}
			}
		} else {
			orgoruserName = strings.Split(splitArray[0], ":")[1]
		}

		switch repoorgist {
		case "repo":
			rn = strings.Split(lastString, ".")[0]
		case "gist":
			rn = lastString
		}
		fpath = bpath + orgoruserName + "/" + rn
		if !*scanOnly {
			//cloning
			Info("Starting to clone: " + url + "\n")
			var wgo sync.WaitGroup
			wgo.Add(1)
			func(url string, fpath string, wgo *sync.WaitGroup) {
				enqueueJob(func() {
					gitclone(url, fpath, wgo)
				})
			}(url, fpath, &wgo)
			wgo.Wait()
			Info("Cloning of: " + url + " finished\n")
		}
		//scanning
		Info("Starting to scan: " + url + "\n")
		var wgs sync.WaitGroup
		wgs.Add(1)

		func(rn string, fpath string, wgs *sync.WaitGroup, orgoruserName string) {
			enqueueJob(func() {
				runGitTools(*toolName, fpath+"/", wgs, rn, orgoruserName)
			})
		}(rn, fpath, &wgs, orgoruserName)

		wgs.Wait()
		Info("Scanning of: " + url + " finished\n")

	}

	//Now, that all the scanning has finished, time to combine the output
	// There are two option here:
	if *mergeOutput {
		// The first is to merge everything in /tmp/results into one JSON file
		Info("Merging the output into one JSON file\n")
		mergeOutputJSON(*outputFile)
	} else {
		// The second is to just concat the outputs
		Info("Combining the output into one file\n")
		err = combineOutput(*toolName, *outputFile)
		check(err)
	}
}
