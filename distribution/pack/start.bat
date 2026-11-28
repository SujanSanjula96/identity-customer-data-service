@echo off
rem Copyright (c) 2026, Choreo Labs LLC. (https://choreolabs.com).
rem
rem Choreo Labs licenses this file to you under the Apache License,
rem Version 2.0 (the "License"); you may not use this file except
rem in compliance with the License.
rem You may obtain a copy of the License at
rem
rem http://www.apache.org/licenses/LICENSE-2.0
rem
rem Unless required by applicable law or agreed to in writing,
rem software distributed under the License is distributed on an
rem "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
rem KIND, either express or implied.  See the License for the
rem specific language governing permissions and limitations
rem under the License.

rem Runs the CDD service in the foreground. Ctrl+C stops it.
rem
rem The Windows counterpart of start.sh: a thin entry point over `cdd start`,
rem kept in bin\ beside it. It works from any directory. Options are passed
rem through:
rem
rem   bin\start.bat            run CDD
rem   bin\start.bat --debug    run CDD, logging at DEBUG level
rem
rem There is no background mode. Running CDD as a service is the job of a
rem service wrapper, which runs this in the foreground too.

setlocal
set "CDD=%~dp0cdd.exe"
rem Not an if-block: a path holding a parenthesis, such as one under
rem "Program Files (x86)", would end the block early.
if not exist "%CDD%" goto missing

"%CDD%" start %*
exit /b %ERRORLEVEL%

:missing
echo start: %CDD% is missing 1>&2
exit /b 1
