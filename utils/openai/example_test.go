package openai_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/ekk1/mygo/utils/openai"
)

func ExampleClient_CreateResponse() {
	client, err := openai.New(openai.Config{APIKey: os.Getenv("OPENAI_API_KEY"), ProxyURL: "-"})
	if err != nil {
		fmt.Println(err)
		return
	}
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	response, err := client.CreateResponse(ctx, openai.ResponseRequest{Model: os.Getenv("OPENAI_MODEL"), Input: "Summarize today's technology news", Tools: []any{openai.WebSearch(openai.WebSearchOptions{})}})
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, item := range response.Output {
		for _, part := range item.Content {
			if part.Type == "output_text" {
				fmt.Println(part.Text)
			}
		}
	}
}

// Upload once, reuse both the remote file and container, then download a cited artifact.
func ExampleClient_CreateContainer() {
	client, err := openai.New(openai.Config{APIKey: os.Getenv("OPENAI_API_KEY"), ProxyURL: "-"})
	if err != nil {
		fmt.Println(err)
		return
	}
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	input, err := os.Open("data.xlsx")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer input.Close()
	file, err := client.UploadFile(ctx, openai.UploadFileParams{File: openai.Upload{Filename: "data.xlsx", Reader: input}, Purpose: "user_data"})
	if err != nil {
		fmt.Println(err)
		return
	}
	container, err := client.CreateContainer(ctx, openai.CreateContainerParams{Name: "spreadsheet-analysis", FileIDs: []string{file.ID}, MemoryLimit: openai.Ptr("4g")})
	if err != nil {
		fmt.Println(err)
		return
	}
	tools := []any{openai.CodeInterpreter(openai.CodeInterpreterOptions{Container: container.ID})}
	response, err := client.CreateResponse(ctx, openai.ResponseRequest{Model: os.Getenv("OPENAI_MODEL"), Input: "Analyze data.xlsx in the container and report the main findings.", Tools: tools})
	if err != nil {
		fmt.Println(err)
		return
	}
	response, err = client.CreateResponse(ctx, openai.ResponseRequest{Model: os.Getenv("OPENAI_MODEL"), PreviousResponseID: response.ID, Input: "Use the same spreadsheet to create report.pptx, and link the downloadable file.", Tools: tools})
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, item := range response.Output {
		for _, part := range item.Content {
			var citations []struct {
				Type        string `json:"type"`
				ContainerID string `json:"container_id"`
				FileID      string `json:"file_id"`
				Filename    string `json:"filename"`
			}
			if len(part.Annotations) == 0 {
				continue
			}
			if err := json.Unmarshal(part.Annotations, &citations); err != nil {
				fmt.Println(err)
				return
			}
			for _, citation := range citations {
				if citation.Type != "container_file_citation" {
					continue
				}
				out, err := os.Create("report.pptx")
				if err != nil {
					fmt.Println(err)
					return
				}
				_, err = client.DownloadContainerFile(ctx, citation.ContainerID, citation.FileID, out)
				closeErr := out.Close()
				if err != nil {
					fmt.Println(err)
				} else if closeErr != nil {
					fmt.Println(closeErr)
				}
				return
			}
		}
	}
	fmt.Println("No downloadable container artifact was returned")
}
