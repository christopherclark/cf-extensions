package bot

import (
	"context"
	"fmt"
	"os"
	"path"
	"time"

	"encoding/json"
	"io/ioutil"
	"text/template"

	"github.com/google/go-github/github"

	"github.com/cloudfoundry-incubator/cf-extensions/models"
	"gopkg.in/yaml.v2"
)

type ExtRepos struct {
	Username string
	Org      string
	Topics   []string
	Client   *github.Client
}

const ISSUE_TITLE = "Add .cf-extensions to your repo to be listed in cloudfoundry-incubator.cf-extensions"

func NewExtRepos(username, org string, topics []string, client *github.Client) *ExtRepos {
	return &ExtRepos{
		Username: username,
		Org:      org,
		Topics:   topics,
		Client:   client,
	}
}

func (extRepos *ExtRepos) GetInfos() ([]models.Info, []models.Info) {
	allRepos := extRepos.getRepos()
	projectsStatus := extRepos.extractProjectsStatus()

	return extRepos.FetchInfos(allRepos, projectsStatus)
}

func (extRepos *ExtRepos) HasTopics(repo *github.Repository, topics []string) bool {
	for _, topic := range topics {
		found := false
		for _, repoTopic := range *repo.Topics {
			if topic == repoTopic {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (extRepos *ExtRepos) DefaultInfo(repo *github.Repository) models.Info {
	info := models.Info{
		Name:   *repo.Name,
		GitUrl: *repo.GitURL,

		LeadCompany:  "ADD LEAD COMPANY HERE",
		ContactEmail: "contact@lead-company.com",

		Description: "ADD DESCRIPTION HERE",

		ProposalUrl: models.PROPOSAL_DEFAULT_URL,
		TrackerUrl:  models.TRACKER_DEFAULT_URL,

		ProposedDate: time.Now().Format(time.RFC3339),

		Repo: repo,
	}

	info.Update()

	return info
}

func (extRepos *ExtRepos) FetchInfos(repos []*github.Repository, projectsStatus models.ProjectsStatus) ([]models.Info, []models.Info) {
	var trackedInfos, untrackedInfos []models.Info
	for _, r := range repos {
		info, err := extRepos.FetchInfo(r)
		if err != nil {
			info = extRepos.DefaultInfo(r)
			untrackedInfos = append(untrackedInfos, info)
			if !extRepos.InfoIssueExists(info) {
				issue, err := extRepos.CreateInfoIssue(info, r)
				if err != nil {
					Printf("ERROR creating default info issue in repo: %s, message: %s\n", info.Name, err.Error())
				}
				Printf("Created default info issue #%d in repo: %s\n", *issue.Number, info.Name)
			} else {
				Printf("Info issue already exists in %s\n", info.Name)
			}
		} else {
			latestRepoRelease, err := extRepos.FetchLatestRepoRelease(r)
			if err != nil {
				Printf("Error getting latest release for repo: %s\n", info.Name)
			} else {
				info.LatestRepoRelease = latestRepoRelease
			}
			info.AddDefaults()
			status, err := projectsStatus.StatusForName(info.Name)
			if err != nil {
				Printf("Error could not find status for `%s` adding to untracked projects\n", info.Name)
				untrackedInfos = append(untrackedInfos, info)
			} else {
				info.Status = status
				trackedInfos = append(trackedInfos, info)
			}
		}
	}
	return trackedInfos, untrackedInfos
}

func (extRepos *ExtRepos) FetchLatestRepoRelease(repo *github.Repository) (*github.RepositoryRelease, error) {
	latestRepoRelease, _, err := extRepos.Client.Repositories.GetLatestRelease(context.Background(), extRepos.Org, *repo.Name)
	if err != nil {
		return nil, err
	}

	return latestRepoRelease, nil
}

func (extRepos *ExtRepos) FetchInfo(repo *github.Repository) (models.Info, error) {
	fileContents, _, _, err := extRepos.Client.Repositories.GetContents(context.Background(),
		extRepos.Org, *repo.Name, ".cf-extensions", &github.RepositoryContentGetOptions{})
	if err != nil {
		return models.Info{}, err
	}

	fileBytes, err := extractFileBytes(fileContents)
	if err != nil || string(fileBytes) == "" {
		return models.Info{}, err
	}

	info := models.Info{Repo: repo}
	err = json.Unmarshal(fileBytes, &info)
	if err != nil {
		Printf("Could not unmarshal `%s` repo info as JSON, trying YAML\n", *repo.Name)
		err = yaml.Unmarshal(fileBytes, &info)
		if err != nil {
			Printf("ERROR unmarshalling `%s` repo info as YAML, giving up\n", *repo.Name)
			return models.Info{}, err
		}
	}
	info.Update()

	return info, nil
}

func (extRepos *ExtRepos) CreateInfoIssue(info models.Info, repo *github.Repository) (*github.Issue, error) {
	info.SlackChannel = "#add-slack-channel-here"
	infoJson, err := extRepos.extractInfoJson(info)
	if err != nil {
		Printf("Could not marshall info into JSON string error: %v\n", err)
		return nil, err
	}

	infoYaml, err := extRepos.extractInfoYaml(info)
	if err != nil {
		Printf("Could not marshall info into YAML string error: %v\n", err)
		return nil, err
	}

	type IssueInfo struct {
		Filename   string
		InfoJson   string
		InfoYaml   string
		TrackerUrl string
		LogoUrl    string
		IconUrl    string
	}

	issueInfo := IssueInfo{
		"`.cf-extensions`",
		fmt.Sprintf("```json\n%s\n```", infoJson),
		fmt.Sprintf("```yaml\n%s\n```", infoYaml),
		"`tracker_url`",
		"`logo_url`",
		"`icon_url`",
	}

	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	issueInfoTmplBytes, err := ioutil.ReadFile(path.Join(wd, "templates", "info_issue.tmpl"))
	if err != nil {
		return nil, err
	}

	issueInfoTemplate, err := template.New("issue-info").Parse(string(issueInfoTmplBytes))
	if err != nil {
		Printf("Could not create issue info error: %v\n", err)
		return nil, err
	}

	tmpFile, err := ioutil.TempFile(os.TempDir(), "cf-extensions")
	defer os.Remove(tmpFile.Name())
	if err != nil {
		return nil, err
	}

	err = issueInfoTemplate.Execute(tmpFile, issueInfo)
	if err != nil {
		return nil, err
	}

	issueInfoContents, err := ioutil.ReadFile(tmpFile.Name())
	if err != nil {
		return nil, err
	}

	issueRequest := github.IssueRequest{
		Title: github.String(ISSUE_TITLE),
		Body:  github.String(string(issueInfoContents)),
	}

	issue, _, err := extRepos.Client.Issues.Create(context.Background(), extRepos.Org, info.Name, &issueRequest)
	if err != nil {
		Printf("Issues.Create returned error: %v\n", err)
		return nil, err
	}

	return issue, nil
}

func (extRepos *ExtRepos) InfoIssueExists(info models.Info) bool {
	issueListByRepoOpts := github.IssueListByRepoOptions{
		State:       "open",
		Creator:     extRepos.Username,
		ListOptions: github.ListOptions{PerPage: 30},
	}

	for {
		issues, resp, err := extRepos.Client.Issues.ListByRepo(context.Background(), extRepos.Org, info.Name, &issueListByRepoOpts)
		if err != nil {
			Printf("Issues.List returned error: %v\n", err)
			return false
		}

		for _, issue := range issues {
			if *issue.Title == ISSUE_TITLE {
				return true
			}
		}

		if resp.NextPage == 0 {
			break
		}

		issueListByRepoOpts.Page = resp.NextPage
	}

	return false
}

// Private methods

func (extRepos *ExtRepos) getRepos() []*github.Repository {
	orgOpts := &github.RepositoryListByOrgOptions{
		ListOptions: github.ListOptions{PerPage: 30},
	}
	var allRepos []*github.Repository
	for {
		repos, resp, err := extRepos.Client.Repositories.ListByOrg(context.Background(),
			extRepos.Org,
			orgOpts)
		if err != nil {
			Printf("err: %s", err.Error())
			os.Exit(1)
		}

		var filteredRepos []*github.Repository
		for _, r := range repos {
			if extRepos.HasTopics(r, extRepos.Topics) {
				filteredRepos = append(filteredRepos, []*github.Repository{r}...)
			}
		}

		allRepos = append(allRepos, filteredRepos...)
		if resp.NextPage == 0 {
			break
		}

		orgOpts.Page = resp.NextPage
	}
	return allRepos
}

func (extRepos *ExtRepos) extractProjectsStatus() models.ProjectsStatus {
	projectsStatusPath := path.Join("data", "projects_status.json")

	fileContents, _, _, err := extRepos.Client.Repositories.GetContents(context.Background(),
		extRepos.Org, "cf-extensions", projectsStatusPath, &github.RepositoryContentGetOptions{})
	if err != nil {
		Printf("Error fetching `%s` with projects status\n", projectsStatusPath)
		return models.ProjectsStatus{}
	}

	fileBytes, err := extractFileBytes(fileContents)
	if err != nil {
		Printf("Error reading `%s` with projects status\n", projectsStatusPath)
		return models.ProjectsStatus{}
	}

	projectsStatus := models.ProjectsStatus{}
	err = json.Unmarshal(fileBytes, &projectsStatus)
	if err != nil {
		Printf("Error unmarshalling projects status, message: %s\n", err.Error())
		return models.ProjectsStatus{}
	}

	return projectsStatus
}

func (extRepos *ExtRepos) extractInfoJson(info models.Info) (string, error) {
	info.Name, info.GitUrl = "", ""
	infoBytes, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return "{}", err
	}

	return string(infoBytes), nil
}

func (extRepos *ExtRepos) extractInfoYaml(info models.Info) (string, error) {
	info.Name, info.GitUrl, info.Repo = "", "", nil
	infoBytes, err := yaml.Marshal(info)
	if err != nil {
		return "---", err
	}

	return string(infoBytes), nil
}
