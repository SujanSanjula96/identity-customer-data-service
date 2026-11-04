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

package cache

import (
	"fmt"
	"testing"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

func TestMain(m *testing.M) {
	_ = log.Init("error")
	m.Run()
}

func TestGetDropsAnExpiredEntry(t *testing.T) {
	c := NewCache(10 * time.Millisecond)
	c.Set("k", "v")

	if _, found := c.Get("k"); !found {
		t.Fatal("entry should be readable before it expires")
	}

	time.Sleep(20 * time.Millisecond)

	if _, found := c.Get("k"); found {
		t.Error("expired entry should not be readable")
	}
	if c.Len() != 0 {
		t.Errorf("reading an expired entry should remove it; %d left", c.Len())
	}
}

// TestExpiredEntriesDoNotAccumulate is the bug this guards: an expired entry was invisible
// to Get but still held its slot, and nothing ever removed it. Keyed by something
// high-cardinality — an email address — the map grew for the life of the process.
func TestExpiredEntriesDoNotAccumulate(t *testing.T) {
	c := NewCache(time.Millisecond)

	for i := 0; i < initialSweepThreshold*3; i++ {
		c.Set(fmt.Sprintf("value-%d@example.com", i), i)
		if i%64 == 0 {
			time.Sleep(2 * time.Millisecond)
		}
	}

	// Entries written long enough ago to have expired must not still be resident. The live
	// set here is tiny, so the cache should be nowhere near the number of keys written.
	if c.Len() >= initialSweepThreshold*2 {
		t.Errorf("cache held %d entries after writing %d short-lived keys; it is not being swept",
			c.Len(), initialSweepThreshold*3)
	}
}

// TestLiveEntriesSurviveASweep checks the sweep only removes what has actually expired.
func TestLiveEntriesSurviveASweep(t *testing.T) {
	c := NewCache(time.Hour)

	for i := 0; i < initialSweepThreshold+50; i++ {
		c.Set(fmt.Sprintf("k%d", i), i)
	}

	if c.Len() != initialSweepThreshold+50 {
		t.Errorf("unexpired entries were dropped: have %d, wrote %d",
			c.Len(), initialSweepThreshold+50)
	}
	if _, found := c.Get("k0"); !found {
		t.Error("the first entry should still be present")
	}
}

func TestSetRefreshesExpiry(t *testing.T) {
	c := NewCache(40 * time.Millisecond)
	c.Set("k", "first")

	time.Sleep(25 * time.Millisecond)
	c.Set("k", "second")
	time.Sleep(25 * time.Millisecond)

	value, found := c.Get("k")
	if !found {
		t.Fatal("re-setting a key should extend its life")
	}
	if value != "second" {
		t.Errorf("value = %v, want the refreshed one", value)
	}
}

func TestConcurrentReadsAndWrites(t *testing.T) {
	c := NewCache(5 * time.Millisecond)
	done := make(chan struct{})

	for w := 0; w < 4; w++ {
		go func(w int) {
			for i := 0; i < 500; i++ {
				c.Set(fmt.Sprintf("w%d-k%d", w, i), i)
				c.Get(fmt.Sprintf("w%d-k%d", w, i/2))
			}
			done <- struct{}{}
		}(w)
	}
	for w := 0; w < 4; w++ {
		<-done
	}
}
