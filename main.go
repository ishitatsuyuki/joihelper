// joihelper project main.go
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strconv"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/fsnotify/fsnotify"
)

type sessionJar struct {
	SessionID string
}

func (s sessionJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
}

func (s sessionJar) Cookies(u *url.URL) []*http.Cookie {
	return []*http.Cookie{&http.Cookie{Name: "JSESSIONID", Value: s.SessionID}}
}

func getCase(c *http.Client, url string) []byte {
	resp, err := c.Get(url)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		content, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			panic(err)
		}
		content = bytes.Replace(content, []byte("\r"), nil, -1)
		return content
	case http.StatusNotFound:
		color.Yellow("404 when downloading URL (missing testcase?): %s", url)
		break
	default:
		panic(fmt.Sprintf("Unexpected HTTP status code: %d", resp.StatusCode))
	}
	return nil
}

func pushResult(c *http.Client, url string, bodyType string, body io.Reader) {
	resp, err := c.Post(url, bodyType, body)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		_, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			panic(err)
		}
	default:
		panic(fmt.Sprintf("Unexpected HTTP status code: %d", resp.StatusCode))
	}
}

func main() {
	session := os.Getenv("JSESSIONID")
	if session == "" {
		panic("Please set the JSESSIONID cookie environment variable")
	}
	term := os.Getenv("JOITR")
	if term == "" {
		panic("Please set environment variable JOITR to the term")
	}

	qNo := flag.Int("q", -1, "The question number")
	executable := flag.String("e", "a.out", "The executable to monitor")
	source := flag.String("s", "main.cpp", "The source file to upload")
	flag.Parse()

	param := url.Values{}
	param.Add("id", fmt.Sprintf("t%d", *qNo))
	param.Add("term", term)
	color.Blue("View the question at: https://www.ioi-jp.org/JOI/auth/showForm.action?%s", param.Encode())

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		panic(err)
	}
	defer watcher.Close()
	err = watcher.Add(path.Dir(*executable))
	if err != nil {
		panic(err)
	}

	color.Green("Fetching test cases...")

	sampleInputs := make([][]byte, 2)
	sampleOutputs := make([][]byte, len(sampleInputs))
	inputs := make([][]byte, 5)

	client := &http.Client{Jar: &sessionJar{SessionID: session}}
	wg := sync.WaitGroup{}
	wg.Add(len(sampleInputs) + len(sampleOutputs))
	for i := 0; i != len(sampleInputs); i++ {
		go func(index int) {
			result := getCase(client, fmt.Sprintf("https://www.ioi-jp.org/JOI/auth/2017-%s-t%d-in_s%d.txt", term, *qNo, index+1))
			sampleInputs[index] = result
			wg.Done()
		}(i)
		go func(index int) {
			result := getCase(client, fmt.Sprintf("https://www.ioi-jp.org/JOI/auth/2017-%s-t%d-out_s%d.txt", term, *qNo, index+1))
			sampleOutputs[index] = result
			wg.Done()
		}(i)
	}
	wg.Add(5)
	for i := 0; i != len(inputs); i++ {
		go func(index int) {
			param := url.Values{}
			param.Add("name", fmt.Sprintf("2017-%s-t%d-in%d.txt", term, *qNo, index+1))
			param.Add("kind", "in")
			param.Add("term", term)
			result := getCase(client, fmt.Sprintf("https://www.ioi-jp.org/JOI/auth/fileDownload.action?%s", param.Encode()))
			inputs[index] = result
			wg.Done()
		}(i)
	}
	wg.Wait()

	for {
		color.Blue("Running tests...")
		passed := 0
		for i := 0; i != len(sampleInputs); i++ {
			if sampleInputs[i] == nil {
				passed++
				continue
			}
			input := bytes.NewBuffer(sampleInputs[i])
			output := &bytes.Buffer{}
			cmd := exec.Cmd{Path: *executable, Stdin: input, Stdout: output, Stderr: os.Stderr}
			err := cmd.Run()
			if err != nil {
				color.Red("Execution FAILED in test %d: %s", i+1, fmt.Errorf("%v", err))
				goto wait
			}
			if bytes.Equal(output.Bytes(), sampleOutputs[i]) {
				passed++
			} else {
				color.Red("Test %d FAILED!", i+1)
				color.Red("Input:")
				os.Stdout.Write(sampleInputs[i])
				color.Red("Expected:")
				os.Stdout.Write(sampleOutputs[i])
				color.Red("Actual:")
				os.Stdout.Write(output.Bytes())
			}
		}
		if passed == len(sampleInputs) {
			color.Green("Test passed.")
			break
		} else {
			color.Red("%d/%d tests passed.", passed, len(sampleInputs))
		}
		color.Blue("Waiting for file change...")
	wait:
		select {
		case event := <-watcher.Events:
			if (event.Op&(fsnotify.Write|fsnotify.Rename)) != 0 && path.Base(event.Name) == path.Base(*executable) {
				color.Green("Detected file change, rerunning tests")
			} else {
				goto wait
			}
		case err := <-watcher.Errors:
			panic(err)
		}
	}

	outputs := make([][]byte, len(inputs))

	color.Blue("Processing inputs...")
	for i := 0; i != len(inputs); i++ {
		input := bytes.NewBuffer(inputs[i])
		output := &bytes.Buffer{}
		cmd := exec.Cmd{Path: *executable, Stdin: input, Stdout: output, Stderr: os.Stderr}
		err := cmd.Run()
		if err != nil {
			color.Red("Execution FAILED in input %d: %s", i+1, fmt.Errorf("%v", err))
			panic(err)
		}
		outputs[i] = output.Bytes()
	}

	color.Blue("Uploading...")
	{
		wg.Add(1)
		pipeRead, pipeWrite := io.Pipe()
		part := multipart.NewWriter(pipeWrite)
		go func() {
			sourceFile, err := os.Open(*source)
			if err != nil {
				panic(err)
			}
			defer sourceFile.Close()
			defer pipeWrite.Close()
			defer part.Close()
			_ = part.WriteField("id", fmt.Sprintf("t%d", *qNo))
			_ = part.WriteField("term", term)
			_ = part.WriteField("formCount", "0")
			_ = part.WriteField("fno", "0")
			writer, err := part.CreateFormFile("program", path.Base(*source))
			if err != nil {
				panic(err)
			}
			_, err = io.Copy(writer, sourceFile)
			if err != nil {
				panic(err)
			}
		}()
		go func() {
			pushResult(client, "https://www.ioi-jp.org/JOI/auth/fileUpload.action", part.FormDataContentType(), pipeRead)
			wg.Done()
		}()
	}
	wg.Add(len(outputs))
	for i := 0; i != len(outputs); i++ {
		pipeRead, pipeWrite := io.Pipe()
		part := multipart.NewWriter(pipeWrite)
		go func(index int) {
			// Workaround rate limit
			time.Sleep(time.Duration(index+1) * time.Second)
			defer pipeWrite.Close()
			defer part.Close()
			_ = part.WriteField("id", fmt.Sprintf("t%d", *qNo))
			_ = part.WriteField("term", term)
			_ = part.WriteField("formCount", strconv.Itoa(index+1))
			_ = part.WriteField("fno", strconv.Itoa(index+1))
			writer, err := part.CreateFormFile(fmt.Sprintf("out%d", index+1), "out.txt")
			if err != nil {
				panic(err)
			}
			_, err = writer.Write(outputs[index])
			if err != nil {
				panic(err)
			}
		}(i)
		go func() {
			pushResult(client, "https://www.ioi-jp.org/JOI/auth/fileUpload.action", part.FormDataContentType(), pipeRead)
			wg.Done()
		}()
	}
	wg.Wait()
	color.Green("Everything OK")
}
