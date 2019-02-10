package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type repositoryScan struct {
	Repository string              `json:"repository"`
	Results    map[string][]string `json:"stringsFound"`
}
type reposupervisorOutput struct {
	Result map[string][]string `json:"result"`
}
type truffleHogOutput struct {
	Branch       string   `json:"branch"`
	Commit       string   `json:"commit"`
	CommitHash   string   `json:"commitHash"`
	Date         string   `json:"date"`
	Diff         string   `json:"diff"`
	Path         string   `json:"path"`
	PrintDiff    string   `json:"printDiff"`
	Reason       string   `json:"reason"`
	StringsFound []string `json:"stringsFound"`
}

func fileExists(file string) bool {
	if _, err := os.Stat(file); os.IsNotExist(err) {
		return false
	}
	return true
}

func runTrufflehog(filepath string, reponame string, orgoruser string) error {
	outputDir := "/tmp/results/" + orgoruser + "/" + reponame
	os.MkdirAll(outputDir, 0700)
	outputFile1 := outputDir + "/" + "truffleHog"

	// open the out file for writing
	outfile, fileErr := os.OpenFile(outputFile1, os.O_CREATE|os.O_RDWR, 0644)
	check(fileErr)
	defer outfile.Close()

	params := []string{filepath, "--rules=/root/truffleHog/rules.json", "--regex"}
	if *mergeOutput {
		params = append(params, "--json")
	}
	var cmd1 *exec.Cmd

	if *thogEntropy {
		params = append(params, "--entropy=True")
	} else {
		params = append(params, "--entropy=False")
	}
	start := time.Now()
	cmd1 = exec.Command("trufflehog", params...)

	// direct stdout to the outfile
	cmd1.Stdout = outfile

	err1 := cmd1.Run()
	// truffleHog returns an exit code 1 if it finds anything
	elapsed := time.Since(start)
	if err1 != nil && err1.Error() != "exit status 1" {
		Info(fmt.Sprintf("truffleHog Scanning failed after: \t%s\t\t for: %s_%s. Please scan it manually.\n", elapsed, orgoruser, reponame))
		fmt.Println(err1)
	} else {
		fmt.Printf("Finished truffleHog Scanning after: \t%s\t\t for: %s_%s\n", elapsed, orgoruser, reponame)
	}

	return nil
}

func runReposupervisor(filepath string, reponame string, orgoruser string) error {
	outputDir := "/tmp/results/" + orgoruser + "/" + reponame
	os.MkdirAll(outputDir, 0700)
	outputFile3 := outputDir + "/" + "repo-supervisor"

	cmd3 := exec.Command("/root/repo-supervisor/runreposupervisor.sh", filepath, outputFile3)
	var out3 bytes.Buffer
	cmd3.Stdout = &out3
	err3 := cmd3.Run()
	if err3 != nil {
		Info("Repo Supervisor Scanning failed for: " + orgoruser + "_" + reponame + ". Please scan it manually.")
		fmt.Println(err3)
	} else {
		fmt.Println("Finished Repo Supervisor Scanning for: " + orgoruser + "_" + reponame)
	}
	return nil
}

func runGitTools(tool string, filepath string, wg *sync.WaitGroup, reponame string, orgoruser string) {
	defer wg.Done()

	switch tool {
	case "all":
		err := runTrufflehog(filepath, reponame, orgoruser)
		check(err)
		err = runReposupervisor(filepath, reponame, orgoruser)
		check(err)

	case "thog":
		err := runTrufflehog(filepath, reponame, orgoruser)
		check(err)

	case "repo-supervisor":
		err := runReposupervisor(filepath, reponame, orgoruser)
		check(err)
	}
}

func scanforeachuser(user string, wg *sync.WaitGroup) {
	defer wg.Done()

	var wguserrepogist sync.WaitGroup
	gituserrepos, _ := ioutil.ReadDir("/tmp/repos/users/" + user)
	for _, f := range gituserrepos {
		wguserrepogist.Add(1)
		func(user string, wg *sync.WaitGroup, wguserrepogist *sync.WaitGroup, f os.FileInfo) {
			enqueueJob(func() {
				runGitTools(*toolName, "/tmp/repos/users/"+user+"/"+f.Name()+"/", wguserrepogist, f.Name(), user)
			})
		}(user, wg, &wguserrepogist, f)
	}
	wguserrepogist.Wait()
}

func toolsOutput(toolname string, of *os.File) error {

	linedelimiter := "----------------------------------------------------------------------------" +
		"----------------------------------------------------------------------------" +
		"----------------------------------------------------------------------------" +
		"----------------------------------------------------------------------------"

	_, err := of.WriteString("Tool: " + toolname + "\n")
	check(err)

	users, _ := ioutil.ReadDir("/tmp/results/")
	for _, user := range users {
		repos, _ := ioutil.ReadDir("/tmp/results/" + user.Name() + "/")
		for _, repo := range repos {
			file, err := os.Open("/tmp/results/" + user.Name() + "/" + repo.Name() + "/" + toolname)
			check(err)

			fi, err := file.Stat()
			check(err)

			if fi.Size() == 0 {
				continue
			} else if fi.Size() > 0 {
				orgoruserstr := user.Name()
				rnamestr := repo.Name()

				_, err1 := of.WriteString("OrgorUser: " + orgoruserstr + " RepoName: " + rnamestr + "\n")
				check(err1)

				if _, err2 := io.Copy(of, file); err2 != nil {
					return err2
				}

				_, err3 := of.WriteString(linedelimiter + "\n")
				check(err3)

				of.Sync()

			}
			defer file.Close()

		}
	}
	return nil
}

func singletoolOutput(toolname string, of *os.File) error {

	users, _ := ioutil.ReadDir("/tmp/results/")
	for _, user := range users {
		repos, _ := ioutil.ReadDir("/tmp/results/" + user.Name() + "/")
		for _, repo := range repos {
			file, err := os.Open("/tmp/results/" + user.Name() + "/" + repo.Name() + "/" + toolname)
			check(err)

			fi, err := file.Stat()
			check(err)

			if fi.Size() == 0 {
				continue
			} else if fi.Size() > 0 {

				if _, err2 := io.Copy(of, file); err2 != nil {
					return err2
				}
				of.Sync()
			}
			defer file.Close()
		}
	}
	return nil
}

func combineOutput(toolname string, outputfile string) error {
	// Read all files in /tmp/results/<tool-name>/ directories for all the tools
	// open a new file and save it in the output directory - outputFile
	// for each results file, write user/org and reponame, copy results from the file in the outputFile, end with some delimiter

	of, err := os.Create(outputfile)
	check(err)

	switch toolname {
	case "all":
		tools := []string{"truffleHog", "repo-supervisor"}

		for _, tool := range tools {
			err = toolsOutput(tool, of)
			check(err)
		}
	case "truffleHog":
		err = singletoolOutput("truffleHog", of)
		check(err)
	case "repo-supervisor":
		err = singletoolOutput("repo-supervisor", of)
		check(err)
	}

	defer func() {
		cerr := of.Close()
		if err == nil {
			err = cerr
		}
	}()

	return nil
}

func mergeOutputJSON(outputfile string) {
	var results []repositoryScan
	var basePaths []string

	if *repoURL != "" || *gistURL != "" {
		basePaths = []string{"/tmp/repos"}
	} else {
		basePaths = []string{"/tmp/repos/org", "/tmp/repos/users", "/tmp/repos/team"}
	}

	for _, basePath := range basePaths {
		users, _ := ioutil.ReadDir(basePath)
		for _, user := range users {
			repos, _ := ioutil.ReadDir("/tmp/results/" + user.Name() + "/")
			for _, repo := range repos {
				repoPath := basePath + "/" + user.Name() + "/" + repo.Name() + "/"
				repoResultsPath := "/tmp/results/" + user.Name() + "/" + repo.Name() + "/"
				reposupvPath := repoResultsPath + "repo-supervisor"
				thogPath := repoResultsPath + "truffleHog"
				reposupvExists := fileExists(reposupvPath)
				thogExists := fileExists(thogPath)
				repoURL, _ := gitRepoURL(repoPath)

				var mergedOut map[string][]string
				if reposupvExists && thogExists {
					reposupvOut, _ := loadReposupvOut(reposupvPath, repoPath)
					thogOut, _ := loadThogOutput(thogPath)
					mergedOut = mergeOutputs(reposupvOut, thogOut)
				} else if reposupvExists {
					mergedOut, _ = loadReposupvOut(reposupvPath, repoPath)
				} else if thogExists {
					mergedOut, _ = loadThogOutput(thogPath)
				}
				if len(mergedOut) > 0 {
					results = append(results, repositoryScan{Repository: repoURL, Results: mergedOut})
				}
			}
		}
	}
	marshalledResults, err := json.Marshal(results)
	check(err)
	err = ioutil.WriteFile(outputfile, marshalledResults, 0644)
	check(err)
}

func appendIfMissing(slice []string, i string) []string {
	for _, ele := range slice {
		if ele == i {
			return slice
		}
	}
	return append(slice, i)
}

func loadThogOutput(outfile string) (map[string][]string, error) {
	results := make(map[string][]string)
	output, err := ioutil.ReadFile(outfile)
	if err != nil {
		return nil, err
	}

	// There was an issue concerning truffleHog's output not being valid JSON
	// https://github.com/dxa4481/truffleHog/issues/95
	// but apparently it was closed without a fix.
	entries := strings.Split(string(output), "\n")
	for _, entry := range entries[:len(entries)-1] {
		var issue truffleHogOutput
		err := json.Unmarshal([]byte(entry), &issue)
		if err != nil {
			return nil, err
		}
		if _, found := results[issue.Path]; found {
			for _, str := range issue.StringsFound {
				results[issue.Path] = appendIfMissing(results[issue.Path], str)
			}
		} else {
			results[issue.Path] = issue.StringsFound
		}

	}
	return results, nil
}

func loadReposupvOut(outfile string, home string) (map[string][]string, error) {
	results := make(map[string][]string)
	output, err := ioutil.ReadFile(outfile)
	if err != nil {
		return nil, err
	}

	var rsupervisorOutput reposupervisorOutput
	json.Unmarshal(output, &rsupervisorOutput)
	for path, stringFound := range rsupervisorOutput.Result {
		relativePath := strings.TrimPrefix(path, home)
		// Make sure there aren't any leading slashes
		fileName := strings.TrimPrefix(relativePath, "/")
		results[fileName] = stringFound
	}

	return results, nil
}

func mergeOutputs(outputA map[string][]string, outputB map[string][]string) map[string][]string {
	for path, stringsFound := range outputA {
		if _, included := outputB[path]; included {
			outputB[path] = append(outputB[path], stringsFound...)
		} else {
			outputB[path] = stringsFound
		}
	}

	return outputB
}

// Moving directory scanning logic out of individual functions
func scanDir(dir string, org string) error {
	var wg sync.WaitGroup

	allRepos, _ := ioutil.ReadDir(dir)
	for _, f := range allRepos {
		wg.Add(1)
		func(f os.FileInfo, wg *sync.WaitGroup, org string) {
			enqueueJob(func() {
				runGitTools(*toolName, dir+f.Name()+"/", wg, f.Name(), org)
			})
		}(f, &wg, org)

	}
	wg.Wait()
	return nil
}

func scanorgrepos(org string) error {
	err := scanDir("/tmp/repos/org/"+org+"/", org)
	check(err)
	return nil
}

func scanTeamRepos(org string) error {
	err := scanDir("/tmp/repos/team/", org)
	check(err)
	return nil
}
