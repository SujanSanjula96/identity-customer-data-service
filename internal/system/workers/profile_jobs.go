/*
 * Copyright (c) 2026, WSO2 LLC. (http://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

package workers

import "sync"

// profileJobs records the profiles this instance is unifying, so that two workers never unify the
// same profile at the same time.
//
// A job for a profile that is already being unified does not wait for a worker of its own. It
// asks the running job to unify the profile once more when it ends, because the running job may
// have read the profile before the change that queued the second job. However many jobs arrive
// meanwhile, the profile is unified at most once more.
type profileJobs struct {
	mu sync.Mutex
	// running maps the key of each profile being unified to whether another job for it arrived.
	running map[string]bool
}

func newProfileJobs() *profileJobs {
	return &profileJobs{running: map[string]bool{}}
}

// claim reports whether the caller is to unify the profile. When the profile is already being
// unified, it asks that job to run once more and returns false.
func (j *profileJobs) claim(key string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()

	if _, running := j.running[key]; running {
		j.running[key] = true
		return false
	}
	j.running[key] = false
	return true
}

// again reports whether another job for the profile arrived while it was being unified, in which
// case the caller unifies it once more. Otherwise it releases the profile.
func (j *profileJobs) again(key string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.running[key] {
		j.running[key] = false
		return true
	}
	delete(j.running, key)
	return false
}

// release lets the profile go without unifying it again, for a job that did not run.
func (j *profileJobs) release(key string) {
	j.mu.Lock()
	defer j.mu.Unlock()

	delete(j.running, key)
}
