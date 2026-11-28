/*
 * Copyright (c) 2026, Choreo Labs LLC. (https://choreolabs.com).
 *
 * Choreo Labs licenses this file to you under the Apache License,
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

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProfileJobsLetOneWorkerUnifyAProfile(t *testing.T) {
	jobs := newProfileJobs()

	require.True(t, jobs.claim("org/p-1"))
	require.False(t, jobs.claim("org/p-1"), "a second worker took a profile that is being unified")
	require.True(t, jobs.claim("org/p-2"), "another profile has to be free to unify")
}

func TestProfileJobsUnifyOnceMoreHoweverManyJobsArrive(t *testing.T) {
	jobs := newProfileJobs()
	require.True(t, jobs.claim("org/p-1"))

	// Three more jobs arrive while the first runs.
	for range 3 {
		require.False(t, jobs.claim("org/p-1"))
	}

	require.True(t, jobs.again("org/p-1"), "the jobs that arrived need one more run")
	require.False(t, jobs.again("org/p-1"), "one more run covers every job that arrived")
	require.True(t, jobs.claim("org/p-1"), "the profile is free once its job ends")
}

func TestProfileJobsReleaseAProfileWhoseJobDidNotRun(t *testing.T) {
	jobs := newProfileJobs()
	require.True(t, jobs.claim("org/p-1"))
	require.False(t, jobs.claim("org/p-1"))

	jobs.release("org/p-1")

	require.True(t, jobs.claim("org/p-1"))
}
