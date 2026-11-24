#!/usr/bin/env python3
"""Generates the JMeter plans T2 to T7 of the CDS performance tests.

Usage: generate-plans.py [<jmeter directory>]   (default: the directory of this file)

Change a plan here, then run this script. Do not edit the generated .jmx files.
T1 (profile/Anonymous_Visitor_Profile_Creation.jmx) is not generated.
"""
import os
import sys
from xml.sax.saxutils import escape

OUT = sys.argv[1] if len(sys.argv) > 1 else os.path.dirname(os.path.abspath(__file__))


def prop(kind, name, value):
    return f'<{kind} name="{name}">{escape(str(value))}</{kind}>'


def s(name, value):
    return prop("stringProp", name, value)


def b(name, value):
    return prop("boolProp", name, "true" if value else "false")


def udv(extra):
    base = [("CDS_HOST", "${__P(host,localhost)}"), ("CDS_PORT", "${__P(port,8900)}"),
            ("IS_HOST", "${__P(isHost,localhost)}"), ("IS_PORT", "${__P(isPort,9443)}"),
            ("ORG", "${__P(org,carbon.super)}"), ("CLIENT_ID", "${__P(clientId,)}"),
            ("CLIENT_SECRET", "${__P(clientSecret,)}")] + extra
    items = "".join(
        f'<elementProp name="{n}" elementType="Argument">{s("Argument.name", n)}{s("Argument.value", v)}'
        f'{s("Argument.metadata", "=")}</elementProp>' for n, v in base)
    return items


def headers(pairs):
    items = "".join(f'<elementProp name="" elementType="Header">{s("Header.name", n)}{s("Header.value", v)}</elementProp>'
                    for n, v in pairs)
    return (f'<HeaderManager guiclass="HeaderPanel" testclass="HeaderManager" testname="Headers" enabled="true">'
            f'<collectionProp name="HeaderManager.headers">{items}</collectionProp></HeaderManager><hashTree/>')


BEARER = [("Content-Type", "application/json"), ("Authorization", "Bearer ${__P(token,)}")]
BASIC_SYNC = [("Content-Type", "application/json"),
              ("Authorization", "Basic ${__groovy((props.getProperty('syncUser') + ':' + props.getProperty('syncPass')).bytes.encodeBase64().toString(),)}")]


def assert_code(codes):
    strings = "".join(s(f"code{c}", c) for c in codes)
    return (f'<ResponseAssertion guiclass="AssertionGui" testclass="ResponseAssertion" testname="Response code" enabled="true">'
            f'<collectionProp name="Asserion.test_strings">{strings}</collectionProp>'
            f'{s("Assertion.custom_message", "")}{s("Assertion.test_field", "Assertion.response_code")}'
            f'{b("Assertion.assume_success", False)}<intProp name="Assertion.test_type">8</intProp>'
            f'</ResponseAssertion><hashTree/>')


def json_extract(var, path, default="NOTFOUND"):
    return (f'<JSONPostProcessor guiclass="JSONPostProcessorGui" testclass="JSONPostProcessor" testname="Extract {var}" enabled="true">'
            f'{s("JSONPostProcessor.referenceNames", var)}{s("JSONPostProcessor.jsonPathExprs", path)}'
            f'{s("JSONPostProcessor.match_numbers", "1")}{s("JSONPostProcessor.defaultValues", default)}'
            f'</JSONPostProcessor><hashTree/>')


def jsr223(kind, name, script):
    gui = "TestBeanGUI"
    return (f'<{kind} guiclass="{gui}" testclass="{kind}" testname="{escape(name)}" enabled="true">'
            f'{s("scriptLanguage", "groovy")}{s("cacheKey", "true")}{s("parameters", "")}{s("filename", "")}'
            f'{s("script", script)}</{kind}>')


def http(name, method, path, body, hdrs, children="", host="${CDS_HOST}", port="${CDS_PORT}"):
    args = ""
    if body is not None:
        args = (f'{b("HTTPSampler.postBodyRaw", True)}<elementProp name="HTTPsampler.Arguments" elementType="Arguments">'
                f'<collectionProp name="Arguments.arguments"><elementProp name="" elementType="HTTPArgument">'
                f'{b("HTTPArgument.always_encode", False)}{s("Argument.value", body)}{s("Argument.metadata", "=")}'
                f'</elementProp></collectionProp></elementProp>')
    else:
        args = ('<elementProp name="HTTPsampler.Arguments" elementType="Arguments">'
                '<collectionProp name="Arguments.arguments"/></elementProp>')
    return (f'<HTTPSamplerProxy guiclass="HttpTestSampleGui" testclass="HTTPSamplerProxy" testname="{escape(name)}" enabled="true">'
            f'{args}{s("HTTPSampler.domain", host)}{s("HTTPSampler.port", port)}{s("HTTPSampler.protocol", "https")}'
            f'{s("HTTPSampler.path", path)}{s("HTTPSampler.method", method)}{b("HTTPSampler.follow_redirects", False)}'
            f'{b("HTTPSampler.use_keepalive", True)}</HTTPSamplerProxy><hashTree>{headers(hdrs)}{children}</hashTree>')


def token_setup(scopes):
    body = "grant_type=client_credentials&scope=" + "%20".join(scopes)
    publish = '''def t = vars.get("accessToken")
if (t == null || t == "NOTOKEN") {
    log.error("No access token from the identity server. Check clientId, clientSecret, and the scopes of the application.")
    prev.setSuccessful(false)
} else {
    props.put("token", t)
}'''
    sampler = http("Setup Get Access Token", "POST", "/t/${ORG}/oauth2/token", body,
                   [("Content-Type", "application/x-www-form-urlencoded"),
                    ("Authorization", 'Basic ${__groovy(("${CLIENT_ID}" + ":" + "${CLIENT_SECRET}").bytes.encodeBase64().toString(),)}')],
                   json_extract("accessToken", "$.access_token", "NOTOKEN") + jsr223("JSR223PostProcessor", "Publish token as property", publish) + "<hashTree/>",
                   host="${IS_HOST}", port="${IS_PORT}")
    return (f'<SetupThreadGroup guiclass="SetupThreadGroupGui" testclass="SetupThreadGroup" testname="Setup" enabled="true">'
            f'{s("ThreadGroup.on_sample_error", "stoptest")}'
            f'<elementProp name="ThreadGroup.main_controller" elementType="LoopController" guiclass="LoopControlPanel" testclass="LoopController" testname="Loop Controller" enabled="true">'
            f'{b("LoopController.continue_forever", False)}{s("LoopController.loops", "1")}</elementProp>'
            f'{s("ThreadGroup.num_threads", "1")}{s("ThreadGroup.ramp_time", "1")}{b("ThreadGroup.scheduler", False)}'
            f'</SetupThreadGroup><hashTree>'
            f'<IfController guiclass="IfControllerPanel" testclass="IfController" testname="If no token is given" enabled="true">'
            f'{s("IfController.condition", "${__groovy(props.getProperty(\"token\") == null || props.getProperty(\"token\").isEmpty())}")}'
            f'{b("IfController.evaluateAll", False)}{b("IfController.useExpression", True)}</IfController>'
            f'<hashTree>{sampler}</hashTree></hashTree>')


def thread_group(name, body):
    return (f'<ThreadGroup guiclass="ThreadGroupGui" testclass="ThreadGroup" testname="{escape(name)}" enabled="true">'
            f'{s("ThreadGroup.on_sample_error", "continue")}'
            f'<elementProp name="ThreadGroup.main_controller" elementType="LoopController" guiclass="LoopControlPanel" testclass="LoopController" testname="Loop Controller" enabled="true">'
            f'{b("LoopController.continue_forever", False)}{s("LoopController.loops", "-1")}</elementProp>'
            f'{s("ThreadGroup.num_threads", "${__P(concurrency,10)}")}{s("ThreadGroup.ramp_time", "${__P(rampUpPeriod,5)}")}'
            f'{b("ThreadGroup.scheduler", True)}{s("ThreadGroup.duration", "${__P(time,60)}")}{s("ThreadGroup.delay", "")}'
            f'{b("ThreadGroup.same_user_on_next_iteration", True)}</ThreadGroup><hashTree>{body}</hashTree>')


def once(body):
    return (f'<OnceOnlyController guiclass="OnceOnlyControllerGui" testclass="OnceOnlyController" testname="Once for each thread" enabled="true"/>'
            f'<hashTree>{body}</hashTree>')


def csv_data(var_names):
    return (f'<CSVDataSet guiclass="TestBeanGUI" testclass="CSVDataSet" testname="Test data (data-gen.py incoming)" enabled="true">'
            f'{s("filename", "${__P(dataFile,)}")}{s("fileEncoding", "UTF-8")}{s("variableNames", var_names)}'
            f'{b("ignoreFirstLine", True)}{s("delimiter", ",")}{b("quotedData", False)}{b("recycle", True)}'
            f'{b("stopThread", False)}{s("shareMode", "shareMode.all")}</CSVDataSet><hashTree/>')


def timer_ms(ms, name=None):
    return (f'<ConstantTimer guiclass="ConstantTimerGui" testclass="ConstantTimer" testname="{escape(name or f"Wait {ms} ms")}" enabled="true">'
            f'{s("ConstantTimer.delay", str(ms))}</ConstantTimer><hashTree/>')


def plan(name, comment, extra_udv, body):
    return ('<?xml version="1.0" encoding="UTF-8"?>\n'
            '<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3"><hashTree>'
            f'<TestPlan guiclass="TestPlanGui" testclass="TestPlan" testname="{escape(name)}" enabled="true">'
            f'{s("TestPlan.comments", comment)}{b("TestPlan.functional_mode", False)}{b("TestPlan.serialize_threadgroups", False)}'
            f'<elementProp name="TestPlan.user_defined_variables" elementType="Arguments" guiclass="ArgumentsPanel" testclass="Arguments" testname="User Defined Variables" enabled="true">'
            f'<collectionProp name="Arguments.arguments">{udv(extra_udv)}</collectionProp></elementProp></TestPlan>'
            f'<hashTree>{body}</hashTree></hashTree></jmeterTestPlan>\n')


PROFILES = "/t/${ORG}/cds/api/v1/profiles"
TRAIT_BODY = ('{"traits":{"email":"${email}","phone":"${phone}","nic":"${nic}","first_name":"${first_name}",'
              '"last_name":"${last_name}","address":"${address}"}}')


def write(rel, text):
    path = os.path.join(OUT, rel)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as handle:
        handle.write(text)
    print(path)


# T2 Profile update ---------------------------------------------------------
t2 = plan(
    "Profile Update",
    "Test case T2. An application updates the profile of a visitor. Each thread makes one "
    "anonymous profile at its start, then sends PATCH /profiles/{id} with 2 traits in a loop. "
    "The traits are not rule attributes, so CDS sends no unification job. Measured: 1 Update Profile.",
    [],
    token_setup(["internal_cds_profile_create", "internal_cds_profile_update", "internal_cds_profile_view"]) +
    thread_group("Applications", once(
        http("0 Create Profile", "POST", PROFILES, "{}", BEARER,
             assert_code(["201"]) + json_extract("profileId", "$.profile_id"))) +
        http("1 Update Profile", "PATCH", PROFILES + "/${profileId}",
             '{"traits":{"last_page":"/page/${__Random(1,500)}","page_views":${__Random(1,100000)}}}',
             BEARER, assert_code(["200"]))))

# T3 Registration sync ------------------------------------------------------
t3 = plan(
    "Registration Sync",
    "Test case T3. A user registers in IS, and IS sends POST_ADD_USER. CDS makes a permanent "
    "profile with the identity attributes. Basic authentication with the CDS sync user "
    "(properties syncUser and syncPass). No call to IS. The values match no profile. "
    "Measured: 1 Registration Sync.",
    [],
    thread_group("Identity Server", http(
        "1 Registration Sync", "POST", PROFILES + "/sync",
        '{"event":"POST_ADD_USER","userId":"${__UUID()}","orgHandle":"${ORG}","claims":{'
        '"http://wso2.org/claims/emailaddress":"reg${__threadNum}.${__counter(FALSE)}.${__time()}@proton.me",'
        '"http://wso2.org/claims/givenname":"Reg${__RandomString(6,abcdefghijklmnop)}",'
        '"http://wso2.org/claims/lastname":"User${__RandomString(8,abcdefghijklmnop)}"}}',
        BASIC_SYNC, assert_code(["200"]))))

# T4 Login sync -------------------------------------------------------------
merge_time = '''// The merge time goes to the variable mergeMs. JMeter writes it as a column of the
// result file (property sample_variables=mergeMs), and summarize.py reads it.
def start = vars.get("loginDone") as long
def end = vars.get("mergeSeen") as long
if (end > 0) {
    vars.put("mergeMs", String.valueOf(end - start))
    SampleResult.setSuccessful(true)
    SampleResult.setResponseCodeOK()
} else {
    vars.put("mergeMs", "")
    SampleResult.setSuccessful(false)
    SampleResult.setResponseCode("504")
    SampleResult.setResponseMessage("No merge after the poll limit")
}'''
poll_check = '''def body = prev.getResponseDataAsString()
// CDS can keep either profile as the master. The anonymous profile then shows
// merged_to (it is the child) or merged_from (it is the master).
if (body.contains("\\"merged_to\\":{") || body.contains("\\"merged_from\\":[{")) {
    vars.put("mergeSeen", String.valueOf(prev.getEndTime()))
}
vars.put("polls", String.valueOf((vars.get("polls") as int) + 1))'''
t4 = plan(
    "Login Sync",
    "Test case T4. A visitor with an anonymous profile logs in. 0: IS registers the user "
    "(POST_ADD_USER, a permanent profile). 1: the application makes an anonymous profile. "
    "2: the visitor does an action (PATCH /profiles/Me with the cds_profile cookie). "
    "3: IS sends AUTHENTICATION_SUCCESS with the cookie. 4: poll GET /profiles/{anonymous id} "
    "each 25 ms for 2 s, then each 250 ms, until the merge shows (merged_to or merged_from), at most 60 s. 5: the time from "
    "the response of step 3 to the first poll that shows the merge. Measured: 3 Login Sync and 5 Time To Merge.",
    [],
    token_setup(["internal_cds_profile_create", "internal_cds_profile_update", "internal_cds_profile_view"]) +
    thread_group("Visitors",
        jsr223("JSR223Sampler", "Prepare iteration",
               'vars.put("userId", UUID.randomUUID().toString())\nvars.put("mergeSeen", "0")\nvars.put("polls", "0")\nvars.put("mergeMs", "")\n'
               'SampleResult.setIgnore()') + "<hashTree/>" +
        http("0 Register User", "POST", PROFILES + "/sync",
             '{"event":"POST_ADD_USER","userId":"${userId}","orgHandle":"${ORG}","claims":{'
             '"http://wso2.org/claims/emailaddress":"login.${userId}@proton.me"}}',
             BASIC_SYNC, assert_code(["200"])) +
        http("1 Create Anonymous Profile", "POST", PROFILES, "{}", BEARER,
             assert_code(["201"]) + json_extract("anonId", "$.profile_id") +
             json_extract("cookie", "$.anonymous_profile_tracker")) +
        http("2 Add Activity", "PATCH", PROFILES + "/Me",
             '{"traits":{"last_page":"/cart","page_views":${__Random(1,1000)}}}',
             BEARER + [("Cookie", "cds_profile=${cookie}")], assert_code(["200"])) +
        http("3 Login Sync", "POST", PROFILES + "/sync",
             '{"event":"AUTHENTICATION_SUCCESS","userId":"${userId}","orgHandle":"${ORG}","profileCookie":"${cookie}"}',
             BASIC_SYNC, assert_code(["200"]) +
             jsr223("JSR223PostProcessor", "Record the login time", 'vars.put("loginDone", String.valueOf(prev.getEndTime()))') + "<hashTree/>") +
        f'<WhileController guiclass="WhileControllerGui" testclass="WhileController" testname="Until merged" enabled="true">'
        f'{s("WhileController.condition", "${__groovy(vars.get(\"mergeSeen\") == \"0\" && (vars.get(\"polls\") as int) < 312)}")}'
        f'</WhileController><hashTree>' +
        http("4 Poll Merge", "GET", PROFILES + "/${anonId}", None, BEARER,
             timer_ms('${__groovy((vars.get("polls") as int) < 80 ? 25 : 250)}',
                      "Wait 25 ms (first 80 polls), then 250 ms") +
             jsr223("JSR223PostProcessor", "Check the merge", poll_check) + "<hashTree/>") +
        '</hashTree>' +
        jsr223("JSR223Sampler", "5 Time To Merge", merge_time) + "<hashTree/>"))

# T5 and T6 Profile link ----------------------------------------------------
t5 = plan(
    "Profile Link",
    "Test cases T5 and T6. 1: the application makes an anonymous profile. 2: it adds the rule "
    "attributes (traits from the data file, data-gen.py incoming). 3: it links the profile to a "
    "new user ID (POST /profiles/link). Steps 2 and 3 send unification jobs. T5 runs with exact "
    "rules and no matching data. T6 runs with fuzzy rules, and 20 % of the rows match a seed "
    "profile with typos. Measured: 3 Link Profile.",
    [],
    token_setup(["internal_cds_profile_create", "internal_cds_profile_update", "internal_cds_profile_view",
                 "internal_cds_profile_link"]) +
    thread_group("Applications",
        csv_data("kind,email,phone,nic,first_name,last_name,address") +
        http("1 Create Profile", "POST", PROFILES, "{}", BEARER,
             assert_code(["201"]) + json_extract("profileId", "$.profile_id")) +
        http("2 Add Attributes", "PATCH", PROFILES + "/${profileId}", TRAIT_BODY, BEARER, assert_code(["200"])) +
        http("3 Link Profile", "POST", PROFILES + "/link",
             '{"profile_id":"${profileId}","user_id":"link-${__UUID()}"}', BEARER, assert_code(["200"]))))

# T7 Unification load -------------------------------------------------------
precise = (f'<PreciseThroughputTimer guiclass="TestBeanGUI" testclass="PreciseThroughputTimer" testname="Arrival rate" enabled="true">'
           f'<doubleProp><name>allowedThroughputSurplus</name><value>1.0</value><savedValue>0.0</savedValue></doubleProp>'
           f'{s("exactLimit", "10000")}{s("throughput", "${__P(rate,10)}")}<intProp name="throughputPeriod">1</intProp>'
           f'{s("duration", "${__P(time,60)}")}<intProp name="batchSize">1</intProp><intProp name="batchThreadDelay">0</intProp>'
           f'<longProp name="randomSeed">0</longProp></PreciseThroughputTimer><hashTree/>')
t7 = plan(
    "Unification Load",
    "Test case T7. Anonymous profiles with rule attributes come in at a fixed rate (property rate, "
    "jobs for each second). Each POST /profiles with traits sends one unification job. The data "
    "file (data-gen.py incoming) has 20 % exact matches, 20 % fuzzy matches, and 60 % no match "
    "against the seed profiles. The measurement of the worker comes from the queue monitor. "
    "Measured here: 1 Create Profile (the API response time at the rate).",
    [],
    token_setup(["internal_cds_profile_create", "internal_cds_profile_view"]) +
    thread_group("Arrivals",
        csv_data("kind,email,phone,nic,first_name,last_name,address") + precise +
        http("1 Create Profile", "POST", PROFILES, TRAIT_BODY, BEARER, assert_code(["201"]))))

write("profile/Profile_Update.jmx", t2)
write("sync/Registration_Sync.jmx", t3)
write("sync/Login_Sync.jmx", t4)
write("profile/Profile_Link.jmx", t5)
write("unification/Unification_Load.jmx", t7)
