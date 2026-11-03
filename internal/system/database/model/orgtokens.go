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

package model

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

// Org tokens let one statement serve every phase of the org handle to org ID migration. A
// statement names the org column through a token, and RenderOrgTokens replaces the token with the
// column, column list or expression of the active phase.
//
// Every statement takes one org argument in every phase: the org key, which is the handle in
// dual_write and the org ID afterwards. Where a phase writes both columns, the other value is read
// from the organizations table, so the $N numbering is the same in every phase.
//
//	{{org}}               the column to filter, join and conflict on
//	{{org_cols}}          the org columns of an INSERT
//	{{org_val $N}}        the values for {{org_cols}}, from the org key $N
//	{{org_set}}           the assignments of an ON CONFLICT ... DO UPDATE SET
//	{{ref_org_cols}}      the reference org columns of a profile_reference INSERT
//	{{ref_org_val $N}}    the values for {{ref_org_cols}}
//	{{org_handle_of X}}   a select-list item named org_handle, for the row alias or table X

// orgTokenMarker starts every token. A statement without it needs no rendering.
const orgTokenMarker = "{{"

var (
	// The argument may carry a cast, for example $3::VARCHAR.
	orgValToken      = regexp.MustCompile(`\{\{(ref_)?org_val (\$\d+(?:::[A-Za-z]+)?)\}\}`)
	orgHandleOfToken = regexp.MustCompile(`\{\{org_handle_of ([A-Za-z_][A-Za-z0-9_]*)\}\}`)
	unknownToken     = regexp.MustCompile(`\{\{[^}]*\}\}`)
)

// orgStaticTokens are the tokens without arguments, per phase.
var orgStaticTokens = map[database.OrgPhase]*strings.Replacer{
	database.OrgPhaseDualWrite: strings.NewReplacer(
		"{{org}}", "org_handle",
		"{{org_cols}}", "org_handle, org_id",
		"{{ref_org_cols}}", "reference_profile_org_handle, reference_profile_org_id",
		"{{org_set}}", "org_handle = EXCLUDED.org_handle, org_id = EXCLUDED.org_id",
	),
	database.OrgPhaseReadOrgId: strings.NewReplacer(
		"{{org}}", "org_id",
		"{{org_cols}}", "org_id, org_handle",
		"{{ref_org_cols}}", "reference_profile_org_id, reference_profile_org_handle",
		"{{org_set}}", "org_id = EXCLUDED.org_id, org_handle = EXCLUDED.org_handle",
	),
	database.OrgPhaseOrgIdOnly: strings.NewReplacer(
		"{{org}}", "org_id",
		"{{org_cols}}", "org_id",
		"{{ref_org_cols}}", "reference_profile_org_id",
		"{{org_set}}", "org_id = EXCLUDED.org_id",
	),
}

// RenderOrgTokens replaces the org tokens of a statement with the text of the phase. A token it does
// not know stays as it is, and the database refuses the statement. It does not panic, because a
// statement can hold text from a request. The tests render every declared statement in every phase
// and check that no token is left (UnknownOrgToken).
func RenderOrgTokens(statement string, phase database.OrgPhase) string {

	if !strings.Contains(statement, orgTokenMarker) {
		return statement
	}

	rendered := orgValToken.ReplaceAllStringFunc(statement, func(token string) string {
		arg := orgValToken.FindStringSubmatch(token)[2]
		if phase.WritesOrgHandle() && !strings.Contains(arg, "::") {
			// The argument is used twice. Without one type on both uses, PostgreSQL deduces text for
			// the comparison and character varying for the column, and refuses the statement.
			arg += "::VARCHAR"
		}
		switch phase {
		case database.OrgPhaseDualWrite:
			return fmt.Sprintf("%s, (SELECT org_id FROM organizations WHERE org_handle = %s)", arg, arg)
		case database.OrgPhaseReadOrgId:
			return fmt.Sprintf("%s, (SELECT org_handle FROM organizations WHERE org_id = %s)", arg, arg)
		default:
			return arg
		}
	})

	rendered = orgHandleOfToken.ReplaceAllStringFunc(rendered, func(token string) string {
		source := orgHandleOfToken.FindStringSubmatch(token)[1]
		if phase == database.OrgPhaseDualWrite {
			return source + ".org_handle"
		}
		return fmt.Sprintf("(SELECT o.org_handle FROM organizations o WHERE o.org_id = %s.org_id) AS org_handle",
			source)
	})

	replacer, ok := orgStaticTokens[phase]
	if !ok {
		replacer = orgStaticTokens[database.DefaultOrgPhase]
	}
	return replacer.Replace(rendered)
}

// UnknownOrgToken returns the first token-like text of a rendered statement, or "" when there is none.
func UnknownOrgToken(rendered string) string {

	return unknownToken.FindString(rendered)
}
