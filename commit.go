// Copyright 2015 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package git

import (
	"bufio"
	"bytes"
	"container/list"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/mcuadros/go-version"

	//New Includes
	"regexp"
)

// Commit represents a git commit.
type Commit struct {
	Tree
	ID            sha1 // The ID of this commit object
	Author        *Signature
	Committer     *Signature
	CommitMessage string

	parents        []sha1 // SHA1 strings
	submoduleCache *objectCache
}

// Message returns the commit message. Same as retrieving CommitMessage directly.
func (c *Commit) Message() string {
	return c.CommitMessage
}

// Summary returns first line of commit message.
func (c *Commit) Summary() string {
	return strings.Split(c.CommitMessage, "\n")[0]
}

// ParentID returns oid of n-th parent (0-based index).
// It returns nil if no such parent exists.
func (c *Commit) ParentID(n int) (sha1, error) {
	if n >= len(c.parents) {
		return sha1{}, ErrNotExist{"", ""}
	}
	return c.parents[n], nil
}

// Parent returns n-th parent (0-based index) of the commit.
func (c *Commit) Parent(n int) (*Commit, error) {
	id, err := c.ParentID(n)
	if err != nil {
		return nil, err
	}
	parent, err := c.repo.getCommit(id)
	if err != nil {
		return nil, err
	}
	return parent, nil
}

// ParentCount returns number of parents of the commit.
// 0 if this is the root commit,  otherwise 1,2, etc.
func (c *Commit) ParentCount() int {
	return len(c.parents)
}

func isImageFile(data []byte) (string, bool) {
	contentType := http.DetectContentType(data)
	if strings.Index(contentType, "image/") != -1 {
		return contentType, true
	}
	return contentType, false
}

func (c *Commit) IsImageFile(name string) bool {
	blob, err := c.GetBlobByPath(name)
	if err != nil {
		return false
	}

	dataRc, err := blob.Data()
	if err != nil {
		return false
	}
	buf := make([]byte, 1024)
	n, _ := dataRc.Read(buf)
	buf = buf[:n]
	_, isImage := isImageFile(buf)
	return isImage
}

// GetCommitByPath return the commit of relative path object.
func (c *Commit) GetCommitByPath(relpath string) (*Commit, error) {
	return c.repo.getCommitByPathWithID(c.ID, relpath)
}

// AddAllChanges marks local changes to be ready for commit.
func AddChanges(repoPath string, all bool, files ...string) error {
	cmd := NewCommand("add")
	if all {
		cmd.AddArguments("--all")
	}
	_, err := cmd.AddArguments(files...).RunInDir(repoPath)
	return err
}

type CommitChangesOptions struct {
	Committer *Signature
	Author    *Signature
	Message   string
}

// CommitChanges commits local changes with given committer, author and message.
// If author is nil, it will be the same as committer.
func CommitChanges(repoPath string, opts CommitChangesOptions) error {
	cmd := NewCommand()
	if opts.Committer != nil {
		cmd.AddEnvs("GIT_COMMITTER_NAME="+opts.Committer.Name, "GIT_COMMITTER_EMAIL="+opts.Committer.Email)
	}
	cmd.AddArguments("commit")

	if opts.Author == nil {
		opts.Author = opts.Committer
	}
	if opts.Author != nil {
		cmd.AddArguments(fmt.Sprintf("--author='%s <%s>'", opts.Author.Name, opts.Author.Email))
	}
	cmd.AddArguments("-m", opts.Message)

	_, err := cmd.RunInDir(repoPath)
	// No stderr but exit status 1 means nothing to commit.
	if err != nil && err.Error() == "exit status 1" {
		return nil
	}
	return err
}

func commitsCount(repoPath, revision, relpath string) (int64, error) {
	var cmd *Command
	isFallback := false
	if version.Compare(gitVersion, "1.8.0", "<") {
		isFallback = true
		cmd = NewCommand("log", "--pretty=format:''")
	} else {
		cmd = NewCommand("rev-list", "--count")
	}
	cmd.AddArguments(revision)
	if len(relpath) > 0 {
		cmd.AddArguments("--", relpath)
	}

	stdout, err := cmd.RunInDir(repoPath)
	if err != nil {
		return 0, err
	}

	if isFallback {
		return int64(strings.Count(stdout, "\n")) + 1, nil
	}
	return strconv.ParseInt(strings.TrimSpace(stdout), 10, 64)
}

// CommitsCount returns number of total commits of until given revision.
func CommitsCount(repoPath, revision string) (int64, error) {
	return commitsCount(repoPath, revision, "")
}

func (c *Commit) CommitsCount() (int64, error) {
	return CommitsCount(c.repo.Path, c.ID.String())
}

func (c *Commit) CommitsByRangeSize(page, size int) (*list.List, error) {
	return c.repo.CommitsByRangeSize(c.ID.String(), page, size)
}

func (c *Commit) CommitsByRange(page int) (*list.List, error) {
	return c.repo.CommitsByRange(c.ID.String(), page)
}

func (c *Commit) CommitsBefore() (*list.List, error) {
	return c.repo.getCommitsBefore(c.ID)
}

func (c *Commit) CommitsBeforeLimit(num int) (*list.List, error) {
	return c.repo.getCommitsBeforeLimit(c.ID, num)
}

func (c *Commit) CommitsBeforeUntil(commitID string) (*list.List, error) {
	endCommit, err := c.repo.GetCommit(commitID)
	if err != nil {
		return nil, err
	}
	return c.repo.CommitsBetween(c, endCommit)
}

func (c *Commit) SearchCommits(keyword string) (*list.List, error) {
	return c.repo.searchCommits(c.ID, keyword)
}

func (c *Commit) GetFilesChangedSinceCommit(pastCommit string) ([]string, error) {
	return c.repo.getFilesChanged(pastCommit, c.ID.String())
}

func (c *Commit) GetSubModules() (*objectCache, error) {
	if c.submoduleCache != nil {
		return c.submoduleCache, nil
	}

	entry, err := c.GetTreeEntryByPath(".gitmodules")
	if err != nil {
		return nil, err
	}
	rd, err := entry.Blob().Data()
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(rd)
	c.submoduleCache = newObjectCache()
	var ismodule bool
	var path string
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "[submodule") {
			ismodule = true
			continue
		}
		if ismodule {
			fields := strings.Split(scanner.Text(), "=")
			k := strings.TrimSpace(fields[0])
			if k == "path" {
				path = strings.TrimSpace(fields[1])
			} else if k == "url" {
				c.submoduleCache.Set(path, &SubModule{path, strings.TrimSpace(fields[1])})
				ismodule = false
			}
		}
	}

	return c.submoduleCache, nil
}

func (c *Commit) GetSubModule(entryname string) (*SubModule, error) {
	modules, err := c.GetSubModules()
	if err != nil {
		return nil, err
	}

	module, has := modules.Get(entryname)
	if has {
		return module.(*SubModule), nil
	}
	return nil, nil
}

// CommitFileStatus represents status of files in a commit.
type CommitFileStatus struct {
	Added    []string
	Removed  []string
	Modified []string
}

func NewCommitFileStatus() *CommitFileStatus {
	return &CommitFileStatus{
		[]string{}, []string{}, []string{},
	}
}

// GetCommitFileStatus returns file status of commit in given repository.
func GetCommitFileStatus(repoPath, commitID string) (*CommitFileStatus, error) {
	stdout, w := io.Pipe()
	done := make(chan struct{})
	fileStatus := NewCommitFileStatus()
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 2 {
				continue
			}

			switch fields[0][0] {
			case 'A':
				fileStatus.Added = append(fileStatus.Added, fields[1])
			case 'D':
				fileStatus.Removed = append(fileStatus.Removed, fields[1])
			case 'M':
				fileStatus.Modified = append(fileStatus.Modified, fields[1])
			}
		}
		done <- struct{}{}
	}()

	stderr := new(bytes.Buffer)
	err := NewCommand("log", "-1", "--name-status", "--pretty=format:''", commitID).RunInDirPipeline(repoPath, w, stderr)
	w.Close() // Close writer to exit parsing goroutine
	if err != nil {
		return nil, concatenateError(err, stderr.String())
	}

	<-done
	return fileStatus, nil
}

// FileStatus returns file status of commit.
func (c *Commit) FileStatus() (*CommitFileStatus, error) {
	return GetCommitFileStatus(c.repo.Path, c.ID.String())
}


//  _________                        .__  __             ________                    .__     
//  \_   ___ \  ____   _____   _____ |__|/  |_  ______  /  _____/___________  ______ |  |__  
//  /    \  \/ /  _ \ /     \ /     \|  \   __\/  ___/ /   \  __\_  __ \__  \ \____ \|  |  \ 
//  \     \___(  <_> )  Y Y  \  Y Y  \  ||  |  \___ \  \    \_\  \  | \// __ \|  |_> >   Y  \
//   \______  /\____/|__|_|  /__|_|  /__||__| /____  >  \______  /__|  (____  /   __/|___|  /
//          \/             \/      \/              \/          \/           \/|__|        \/ 


type CommitsPerUser struct {
	NumCommits int64
	Date       string
	User       string
}

type CommitsInfo struct {
	Info  []*CommitsPerUser
	Total int64
}

func commitsCountPerCollab(repoPath, user string) (*CommitsInfo, error) {
	var cmd *Command

	cmd = NewCommand("log", "--author='"+user+"'", "| grep Date ", "| awk '{print\":\"$4\"-\"$3\"-\"$6}'", "| uniq -c")
	stdout, err := cmd.RunPipesInDir(repoPath)
	if err != nil {
		return nil, err
	}

	if user == "" {
		user = "all"
	}

	commits := &CommitsInfo{
		Info:  []*CommitsPerUser{},
		Total: 0,
	}

	lines := strings.Split(stdout, "\n")
	lines = lines[:len(lines)-1]

	if len(lines) > 0 {
		for _, line := range lines {
			info := strings.SplitN(line, ":", 2)
			numcommits, _ := strconv.ParseInt(strings.TrimSpace(info[0]), 10, 64)
			//fmt.Printf("Numcommits :%d   -----  Date : %s   :      User: %s\n", numcommits, info[1], user)
			commits.Info = append(commits.Info, &CommitsPerUser{
				NumCommits: numcommits,
				Date:       info[1],
				User:       user,
			})
			commits.Total += numcommits
		}
	} else {
		commits.Info = append(commits.Info, &CommitsPerUser{
			NumCommits: 0,
			Date:       "00-000-0000",
			User:       user,
		})
		commits.Total = 0
	}
	return commits, nil
}

func (c *Commit) CommitsCountPerCollab(user string) (*CommitsInfo, error) {
	return commitsCountPerCollab(c.repo.Path, user)
}

type StatsUser struct {
	Insertions int64
	Deletions  int64
	Author     string
	Files      int
}

func numStatCommitsPerUser(user, repoPath string) (*StatsUser, error) {
	var cmd *Command
	cmd = NewCommand("log", "--numstat")
	cmd.AddArguments("--pretty=tformat:")
	cmd.AddArguments("--author=" + user)
	cmd.AddArguments("--until=now")

	stdout, err := cmd.RunInDir(repoPath)
	if err != nil {
		fmt.Printf("ERROR: %v", err)
		return nil, err
	}

	//stats := StatsUser{}
	lines := strings.Split(stdout, "\n")
	lines = lines[:len(lines)-1]
	var insertions, deletions int64

	for _, line := range lines {
		re := regexp.MustCompile("[0-9]+")
		numeros := re.FindAllString(line, -1)
		var ins, del int64
		ins = 0
		del = 0

		if len(numeros) == 2 {
			ins, err = strconv.ParseInt(strings.TrimSpace(numeros[0]), 10, 64)
			if err != nil {
				ins = 0
			}
			del, err = strconv.ParseInt(strings.TrimSpace(numeros[1]), 10, 64)
			if err != nil {
				del = 0
			}
		}

		insertions = insertions + ins
		deletions = deletions + del
	}

	st := &StatsUser{
		Insertions: insertions,
		Deletions:  deletions,
		Author:     user,
		Files:      len(lines),
	}

	return st, err
}

func (c *Commit) NumStatCommitsPerUser(user string) (*StatsUser, error) {
	return numStatCommitsPerUser(user, c.repo.Path)
}
