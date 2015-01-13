// Copyright (c) 2015, B3log
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package output

import (
	"bufio"
	"encoding/json"
	"math/rand"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/b3log/wide/conf"
	"github.com/b3log/wide/session"
	"github.com/b3log/wide/util"
)

// RunHandler handles request of executing a binary file.
func RunHandler(w http.ResponseWriter, r *http.Request) {
	data := map[string]interface{}{"succ": true}
	defer util.RetJSON(w, r, data)

	var args map[string]interface{}

	if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
		logger.Error(err)
		data["succ"] = false
	}

	sid := args["sid"].(string)
	wSession := session.WideSessions.Get(sid)
	if nil == wSession {
		data["succ"] = false
	}

	filePath := args["executable"].(string)
	curDir := filepath.Dir(filePath)

	cmd := exec.Command(filePath)
	cmd.Dir = curDir

	if conf.Docker {
		setNamespace(cmd)
	}

	stdout, err := cmd.StdoutPipe()
	if nil != err {
		logger.Error(err)
		data["succ"] = false
	}

	stderr, err := cmd.StderrPipe()
	if nil != err {
		logger.Error(err)
		data["succ"] = false
	}

	outReader := bufio.NewReader(stdout)
	errReader := bufio.NewReader(stderr)

	if err := cmd.Start(); nil != err {
		logger.Error(err)
		data["succ"] = false
	}

	wsChannel := session.OutputWS[sid]

	channelRet := map[string]interface{}{}

	if !data["succ"].(bool) {
		if nil != wsChannel {
			channelRet["cmd"] = "run-done"
			channelRet["output"] = ""

			err := wsChannel.WriteJSON(&channelRet)
			if nil != err {
				logger.Error(err)
				return
			}

			wsChannel.Refresh()
		}

		return
	}

	channelRet["pid"] = cmd.Process.Pid

	// add the process to user's process set
	processes.add(wSession, cmd.Process)

	go func(runningId int) {
		defer util.Recover()
		defer cmd.Wait()

		logger.Debugf("User [%s, %s] is running [id=%d, file=%s]", wSession.Username, sid, runningId, filePath)

		// push once for front-end to get the 'run' state and pid
		if nil != wsChannel {
			channelRet["cmd"] = "run"
			channelRet["output"] = ""
			err := wsChannel.WriteJSON(&channelRet)
			if nil != err {
				logger.Error(err)
				return
			}

			wsChannel.Refresh()
		}

		go func() {
			for {
				r, _, err := outReader.ReadRune()

				if nil == session.OutputWS[sid] {
					break
				}

				wsChannel := session.OutputWS[sid]

				buf := string(r)
				buf = strings.Replace(buf, "<", "&lt;", -1)
				buf = strings.Replace(buf, ">", "&gt;", -1)

				if nil != err {
					// remove the exited process from user process set
					processes.remove(wSession, cmd.Process)

					logger.Tracef("User [%s, %s] 's running [id=%d, file=%s] has done [stdout %v], ", wSession.Username, sid, runningId, filePath, err)

					if nil != wsChannel {
						channelRet["cmd"] = "run-done"
						channelRet["output"] = buf
						err := wsChannel.WriteJSON(&channelRet)
						if nil != err {
							logger.Error(err)
							break
						}

						wsChannel.Refresh()
					}

					break
				} else {
					if nil != wsChannel {
						channelRet["cmd"] = "run"
						channelRet["output"] = buf
						err := wsChannel.WriteJSON(&channelRet)
						if nil != err {
							logger.Error(err)
							break
						}

						wsChannel.Refresh()
					}
				}
			}
		}()

		for {
			r, _, err := errReader.ReadRune()

			if nil != err || nil == session.OutputWS[sid] {
				break
			}

			wsChannel := session.OutputWS[sid]

			buf := string(r)
			buf = strings.Replace(buf, "<", "&lt;", -1)
			buf = strings.Replace(buf, ">", "&gt;", -1)

			channelRet["cmd"] = "run"
			channelRet["output"] = "<span class='stderr'>" + buf + "</span>"
			err = wsChannel.WriteJSON(&channelRet)
			if nil != err {
				logger.Error(err)
				break
			}

			wsChannel.Refresh()
		}
	}(rand.Int())
}

// StopHandler handles request of stoping a running process.
func StopHandler(w http.ResponseWriter, r *http.Request) {
	data := map[string]interface{}{"succ": true}
	defer util.RetJSON(w, r, data)

	var args map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
		logger.Error(err)
		data["succ"] = false

		return
	}

	sid := args["sid"].(string)
	pid := int(args["pid"].(float64))

	wSession := session.WideSessions.Get(sid)
	if nil == wSession {
		data["succ"] = false

		return
	}

	processes.kill(wSession, pid)
}