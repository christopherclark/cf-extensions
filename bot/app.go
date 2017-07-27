package bot

import (
	"context"
	"fmt"
	"os"
	"path"
	"sort"

	"encoding/json"
	"html/template"
	"io/ioutil"

	"golang.org/x/oauth2"

	"github.com/google/go-github/github"

	"github.com/maximilien/cf-extensions/models"
)

type App struct {
	accessToken string
	Username    string
	Email       string
	Client      *github.Client
}

func NewApp(accessToken, username, email string) *App {
	app := &App{
		accessToken: accessToken,
		Username:    username,
		Email:       email,
	}

	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: app.accessToken})
	app.Client = github.NewClient(oauth2.NewClient(context.Background(), ts))

	return app
}

func (app *App) Run(org string, topics []string) {
	extRepos := NewExtRepos("cloudfoundry-incubator",
		[]string{"cf-extensions"},
		app.Client)
	infos := extRepos.GetInfos()
	sort.Sort(models.Infos(infos))

	projects := models.Projects{Org: extRepos.Org, Infos: infos}
	err := app.PushJsonDb(projects)
	if err != nil {
		fmt.Printf("ERROR: saving / pushing projects file: %s\n", err.Error())
	}

	err = app.GenerateMarkdown(projects)
	if err != nil {
		fmt.Printf("ERROR: generating markdown file for projects: %s\n", err.Error())
	}

	print(extRepos.Org, infos)

}

func (app *App) GenerateMarkdown(projects models.Projects) error {
	fileContents, _, _, err := app.Client.Repositories.GetContents(
		context.Background(),
		"cloudfoundry-incubator",
		"cf-extensions",
		"data/projects.json",
		&github.RepositoryContentGetOptions{})
	if err != nil {
		return err
	}

	if !hasProjectsChanged(projects, fileContents) {
		fmt.Printf("Commited projects.md has not changed, last commit SHA: %s\n", *fileContents.SHA)
		return nil
	}

	funcMap := template.FuncMap{
		"Length":           Length,
		"CurrentTime":      CurrentTime,
		"FormatAsDate":     FormatAsDate,
		"FormatAsDateTime": FormatAsDateTime,
		"ParseAsDate":      ParseAsDate,
	}

	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	templatePath := path.Join(wd, "templates", "cf-extensions.md.tmpl")
	t := template.Must(template.New("cf-extensions.md.tmpl").Funcs(funcMap).ParseFiles(templatePath))

	tmpFile, err := ioutil.TempFile(os.TempDir(), "cf-extensions")
	defer os.Remove(tmpFile.Name())
	if err != nil {
		return err
	}

	err = t.Execute(tmpFile, projects)
	if err != nil {
		return err
	}

	projectsMdFileContents, _, _, err := app.Client.Repositories.GetContents(
		context.Background(),
		"cloudfoundry-incubator",
		"cf-extensions",
		"docs/projects.md",
		&github.RepositoryContentGetOptions{})
	if err != nil {
		return err
	}

	contents, err := ioutil.ReadFile(tmpFile.Name())
	if err != nil {
		return err
	}

	message := "Updating cf-extensions projects.md file"
	repositoryContentsOptions := &github.RepositoryContentFileOptions{
		Message:   &message,
		Content:   contents,
		SHA:       projectsMdFileContents.SHA,
		Committer: &github.CommitAuthor{Name: github.String(app.Username), Email: github.String(app.Email)},
	}

	updateResponse, _, err := app.Client.Repositories.UpdateFile(
		context.Background(),
		"cloudfoundry-incubator",
		"cf-extensions",
		"docs/projects.md",
		repositoryContentsOptions)
	if err != nil {
		fmt.Printf("Repositories.UpdateFile returned error: %v", err)
		return err
	}

	fmt.Printf("Commited projects.md %s\n", *updateResponse.Commit.SHA)

	return nil
}

func (app *App) PushJsonDb(projects models.Projects) error {
	fileContents, _, _, err := app.Client.Repositories.GetContents(
		context.Background(),
		"cloudfoundry-incubator",
		"cf-extensions",
		"data/projects.json",
		&github.RepositoryContentGetOptions{})
	if err != nil {
		return err
	}

	if !hasProjectsChanged(projects, fileContents) {
		fmt.Printf("Commited projects.json has not changed, last commit SHA: %s\n", *fileContents.SHA)
		return nil
	}

	tmpFile, err := ioutil.TempFile(os.TempDir(), "cf-extensions")
	defer os.Remove(tmpFile.Name())
	if err != nil {
		return err
	}

	contents, err := json.MarshalIndent(projects, "", "  ")
	if err != nil {
		return err
	}

	message := "Updating cf-extensions repos info"
	repositoryContentsOptions := &github.RepositoryContentFileOptions{
		Message:   &message,
		Content:   contents,
		SHA:       fileContents.SHA,
		Committer: &github.CommitAuthor{Name: github.String(app.Username), Email: github.String(app.Email)},
	}

	updateResponse, _, err := app.Client.Repositories.UpdateFile(
		context.Background(),
		"cloudfoundry-incubator",
		"cf-extensions",
		"data/projects.json",
		repositoryContentsOptions)
	if err != nil {
		fmt.Printf("Repositories.UpdateFile returned error: %v", err)
		return err
	}

	fmt.Printf("Commited projects.json %s\n", *updateResponse.Commit.SHA)

	return nil
}

// Private utility functions

func print(org string, infos []models.Info) {
	sort.Sort(models.Infos(infos))

	fmt.Println()
	fmt.Printf("Repos for %s, total: %d\n", org, len(infos))
	fmt.Println("-----------------\n")
	for _, info := range infos {
		fmt.Printf("Repo name: %s, URL: %s\n", *info.Repo.Name, *info.Repo.GitURL)
		fmt.Printf("Topics:     %s\n", *info.Repo.Topics)
		fmt.Println()
	}
	fmt.Println("-----------------\n")
	fmt.Printf("Total repos: %d\n", len(infos))
}

func hasProjectsChanged(projects models.Projects, fileContent *github.RepositoryContent) bool {
	fileBytes, err := extractFileBytes(fileContent)
	if err != nil {
		return true
	}

	downloadedProjects := models.Projects{}
	err = json.Unmarshal(fileBytes, &downloadedProjects)
	if err != nil {
		return true
	}

	return projects.Equal(downloadedProjects) != true
}