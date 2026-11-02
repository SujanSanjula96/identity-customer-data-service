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

package scripts

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"
)

// TestEveryQueryIsRegistered keeps AllQueries honest.
//
// The registry is hand-maintained, and everything that checks statements for real — SQLite
// preparation, PostgreSQL preparation, duplicate ids — iterates it. A statement left out is
// therefore not merely unlisted: it is exempt from every one of those checks while looking
// exactly as covered as the rest. A whole feature's worth of statements once reached a
// release candidate this way, none of them ever prepared against the inbuilt datasource.
func TestEveryQueryIsRegistered(t *testing.T) {
	declared := declaredQueryNames(t)
	registered := AllQueries()

	for _, name := range declared {
		if _, ok := registered[name]; !ok {
			t.Errorf("%s is declared with newQuery but missing from AllQueries; "+
				"nothing prepares or validates it", name)
		}
	}

	for name := range registered {
		var found bool
		for _, declaredName := range declared {
			if declaredName == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("AllQueries lists %s, which is not declared in this package", name)
		}
	}
}

// declaredQueryNames returns every package-level variable initialised with newQuery.
func declaredQueryNames(t *testing.T) []string {
	t.Helper()

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(info fs.FileInfo) bool {
		return strings.HasSuffix(info.Name(), ".go") && !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}

	var names []string
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				genDecl, ok := decl.(*ast.GenDecl)
				if !ok || genDecl.Tok != token.VAR {
					continue
				}
				for _, spec := range genDecl.Specs {
					valueSpec, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, value := range valueSpec.Values {
						call, ok := value.(*ast.CallExpr)
						if !ok {
							continue
						}
						fn, ok := call.Fun.(*ast.Ident)
						if !ok || fn.Name != "newQuery" {
							continue
						}
						names = append(names, valueSpec.Names[i].Name)
					}
				}
			}
		}
	}

	if len(names) == 0 {
		t.Fatal("found no newQuery declarations; the parser is not seeing the package source")
	}
	return names
}
