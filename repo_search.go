// Copyright 2015 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package git

import (
	"bufio"
	//"fmt"
	"strings"
	//"strconv"
)



type Match struct {
	CommitID 	string
	Path 		string
	Content 	string
}

type MatchesResults struct {
	NumberMatches int64
	Results 	[]* Match
}

type RepoSearchOptions struct {
	Keyword  string
	OwnerID  int64
	OrderBy  string
	Page     int
	PageSize int // Can be smaller than or equal to setting.ExplorePagingNum
}


//get number of matches from code search
func getNumberOfCodeMatches(repoPath, keyword string) (int64, error){
	var cmd *Command
	cmd = NewCommand("rev-list", "--all", "| xargs git grep -F -c -I '" + keyword + "'" )
	
	stdout, err := cmd.RunPipesInDir(repoPath)
	if len(stdout) <= 0 {
		return 0, nil
	}

	return int64(len(strings.Split(stdout, "\n")) - 1), err
}

func (repo *Repository) GetNumberOfCodeMatches(keyword string) (int64, error) {
	return getNumberOfCodeMatches(repo.Path, keyword)
}

func getRangeOfMatches(repoPath string, opts *RepoSearchOptions) ([]* Match, error){
	var (
		cmd *Command
		matches []* Match
		info []string
		stdout string
		err error
	)

	//fmt.Println("%+v", opts)
	cmd = NewCommand("rev-list", "--all", opts.OrderBy, "| xargs git grep -F -I -i -n --no-color --full-name --break --heading -B 2 -A 2 '" + opts.Keyword + "'")
	
	stdout, err = cmd.RunPipesInDir(repoPath)

	if len(stdout) <= 0 {
		return nil, nil
	}
	results := strings.Split(stdout, "\n\n")

	var limit int64
	if (opts.Page * opts.PageSize) < len(results){
		limit = int64(opts.Page * opts.PageSize)
	} else {
		limit = int64(len(results))
	}

	results = results[(opts.Page - 1) * opts.PageSize : limit]

	for _, result := range  results {
		scanner := bufio.NewReader(strings.NewReader(result))
		header, err := scanner.ReadString('\n')
		if err != nil {
			return nil, err
		}

		info = strings.Split(header, ":")

		matches = append(matches, &Match{
			CommitID: info[0],
			Path: strings.Trim(info[1]," "),
			Content: result,
		})	
	}
	return matches, err
}


func (repo *Repository) GetRangeOfMatches(opts *RepoSearchOptions) ([]* Match, error) {
	return getRangeOfMatches(repo.Path, opts)
}



func (repo *Repository) ShearchMatchesThisRepo(opts *RepoSearchOptions) (matches *MatchesResults, _ error) {
	
	var err error
	matches = new(MatchesResults)

	matches.NumberMatches, err = repo.GetNumberOfCodeMatches(opts.Keyword)
	if err != nil {
		return nil, err
	}

	matches.Results, err = repo.GetRangeOfMatches(opts)
	if err != nil {
		return nil, err
	}

	return matches, nil
}