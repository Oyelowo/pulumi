package test

import (
	"bufio"
	"bytes"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/blang/semver"
	"github.com/hashicorp/hcl/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulumi/pulumi/pkg/v3/codegen"
	"github.com/pulumi/pulumi/pkg/v3/codegen/hcl2/syntax"
	"github.com/pulumi/pulumi/pkg/v3/codegen/pcl"
	"github.com/pulumi/pulumi/pkg/v3/codegen/testing/utils"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/cmdutil"
	"github.com/pulumi/pulumi/sdk/v3/go/common/workspace"
)

var allProgLanguages = codegen.NewStringSet("dotnet", "python", "go", "nodejs")

type ProgramTest struct {
	Directory      string
	Description    string
	Skip           codegen.StringSet
	ExpectNYIDiags codegen.StringSet
	SkipCompile    codegen.StringSet
	// optional map of (mock plugin name to versions) to load for specific tests.
	MockPluginVersions map[string]string
}

var testdataPath = filepath.Join("..", "testing", "test", "testdata")

var PulumiPulumiProgramTests = []ProgramTest{
	{
		Directory:   "assets-archives",
		Description: "Assets and archives",
	},
	{
		Directory:   "synthetic-resource-properties",
		Description: "Synthetic resource properties",
		SkipCompile: codegen.NewStringSet("nodejs", "dotnet", "go"), // not a real package
	},
	{
		Directory:      "aws-s3-folder",
		Description:    "AWS S3 Folder",
		ExpectNYIDiags: allProgLanguages.Except("go"),
		SkipCompile:    allProgLanguages.Except("dotnet"),
		// Blocked on python: TODO[pulumi/pulumi#8062]: Re-enable this test.
		// Blocked on go:
		//   TODO[pulumi/pulumi#8064]
		//   TODO[pulumi/pulumi#8065]
		// Blocked on nodejs: TODO[pulumi/pulumi#8063]
	},
	{
		Directory:   "aws-eks",
		Description: "AWS EKS",
		SkipCompile: codegen.NewStringSet("nodejs"),
		// Blocked on nodejs: TODO[pulumi/pulumi#8067]
	},
	{
		Directory:   "aws-fargate",
		Description: "AWS Fargate",

		// TODO[pulumi/pulumi#8440]
		SkipCompile: codegen.NewStringSet("go"),
	},
	{
		Directory:   "aws-s3-logging",
		Description: "AWS S3 with logging",
		SkipCompile: allProgLanguages.Except("python").Except("dotnet"),
		// Blocked on nodejs: TODO[pulumi/pulumi#8068]
		// Flaky in go: TODO[pulumi/pulumi#8123]
	},
	{
		Directory:   "aws-iam-policy",
		Description: "AWS IAM Policy",
	},
	{
		Directory:   "aws-optionals",
		Description: "AWS get invoke with nested object constructor that takes an optional string",
		// Testing Go behavior exclusively:
		Skip: allProgLanguages.Except("go"),
	},
	{
		Directory:   "aws-webserver",
		Description: "AWS Webserver",
		SkipCompile: codegen.NewStringSet("go"),
		// Blocked on go: TODO[pulumi/pulumi#8070]
	},
	{
		Directory:   "azure-native",
		Description: "Azure Native",
		Skip:        codegen.NewStringSet("go"),
		// Blocked on TODO[pulumi/pulumi#8123]
		SkipCompile: codegen.NewStringSet("go", "nodejs", "dotnet"),
		// Blocked on go:
		//   TODO[pulumi/pulumi#8072]
		//   TODO[pulumi/pulumi#8073]
		//   TODO[pulumi/pulumi#8074]
		// Blocked on nodejs:
		//   TODO[pulumi/pulumi#8075]
	},
	{
		Directory:   "azure-sa",
		Description: "Azure SA",
	},
	{
		Directory:   "kubernetes-operator",
		Description: "K8s Operator",
	},
	{
		Directory:   "kubernetes-pod",
		Description: "K8s Pod",
		SkipCompile: codegen.NewStringSet("go", "nodejs"),
		// Blocked on go:
		//   TODO[pulumi/pulumi#8073]
		//   TODO[pulumi/pulumi#8074]
		// Blocked on nodejs:
		//   TODO[pulumi/pulumi#8075]
	},
	{
		Directory:   "kubernetes-template",
		Description: "K8s Template",
	},
	{
		Directory:   "random-pet",
		Description: "Random Pet",
	},
	{
		Directory:   "aws-resource-options",
		Description: "Resource Options",
		MockPluginVersions: map[string]string{
			"aws": "4.38.0",
		},
	},
	{
		Directory:   "aws-secret",
		Description: "Secret",
	},
	{
		Directory:   "functions",
		Description: "Functions",
	},
	{
		Directory:   "output-funcs-aws",
		Description: "Output Versioned Functions",
	},
	{
		Directory:   "third-party-package",
		Description: "Ensuring correct imports for third party packages",
		// compiling and type checking involves downloading the real package to
		// check against. Because we are checking against the "other" package
		// (which doesn't exist), this does not work.
		SkipCompile: codegen.NewStringSet("nodejs", "dotnet", "go"),
	},
	{
		Directory:   "invalid-go-sprintf",
		Description: "Regress invalid Go",
		Skip:        codegen.NewStringSet("python", "nodejs", "dotnet"),
	},
	{
		Directory:   "typed-enum",
		Description: "Supply strongly typed enums",
		Skip:        codegen.NewStringSet(golang),
	},
	{
		Directory:   "pulumi-stack-reference",
		Description: "StackReference as resource",
	},
	{
		Directory:   "python-resource-names",
		Description: "Repro for #9357",
		Skip:        codegen.NewStringSet("go", "nodejs", "dotnet"),
	},
	{
		Directory:   "logical-name",
		Description: "Logical names",
	},
	{
		Directory:   "aws-lambda",
		Description: "AWS Lambdas",
		// We have special testing for this case because lambda is a python keyword.
		Skip: codegen.NewStringSet("go", "nodejs", "dotnet"),
	},
	{
		Directory:   "discriminated-union",
		Description: "Discriminated Unions for choosing an input type",
		Skip:        codegen.NewStringSet("go"),
		// Blocked on go: TODO[pulumi/pulumi#10834]
	},
}

// Checks that a generated program is correct
//
// The arguments are to be read:
// (Testing environment, path to generated code, set of dependencies)
type CheckProgramOutput = func(*testing.T, string, codegen.StringSet)

// Generates a program from a pcl.Program
type GenProgram = func(program *pcl.Program) (map[string][]byte, hcl.Diagnostics, error)

// Generates a project from a pcl.Program
type GenProject = func(directory string, project workspace.Project, program *pcl.Program) error

type ProgramCodegenOptions struct {
	Language   string
	Extension  string
	OutputFile string
	Check      CheckProgramOutput
	GenProgram GenProgram
	TestCases  []ProgramTest

	// For generating a full project
	IsGenProject bool
	GenProject   GenProject
	// Maps a test file (i.e. "aws-resource-options") to a struct containing a package
	// (i.e. "github.com/pulumi/pulumi-aws/sdk/v5", "pulumi-aws) and its
	// version prefixed by an operator (i.e. " v5.11.0", ==5.11.0")
	ExpectedVersion map[string]PkgVersionInfo
	DependencyFile  string
}

type PkgVersionInfo struct {
	Pkg          string
	OpAndVersion string
}

// TestProgramCodegen runs the complete set of program code generation tests against a particular
// language's code generator.
//
// A program code generation test consists of a PCL file (.pp extension) and a set of expected outputs
// for each language.
//
// The PCL file is the only piece that must be manually authored. Once the schema has been written, the expected outputs
// can be generated by running `PULUMI_ACCEPT=true go test ./..." from the `pkg/codegen` directory.
// nolint: revive
func TestProgramCodegen(
	t *testing.T,
	testcase ProgramCodegenOptions,
) {
	if runtime.GOOS == "windows" {
		t.Skip("TestProgramCodegen is skipped on Windows")
	}

	assert.NotNil(t, testcase.TestCases, "Caller must provide test cases")
	pulumiAccept := cmdutil.IsTruthy(os.Getenv("PULUMI_ACCEPT"))
	skipCompile := cmdutil.IsTruthy(os.Getenv("PULUMI_SKIP_COMPILE_TEST"))
	for _, tt := range testcase.TestCases {
		tt := tt // avoid capturing loop variable
		t.Run(tt.Directory, func(t *testing.T) {
			t.Parallel()
			var err error
			if tt.Skip.Has(testcase.Language) {
				t.Skip()
				return
			}

			expectNYIDiags := tt.ExpectNYIDiags.Has(testcase.Language)

			testDir := filepath.Join(testdataPath, tt.Directory+"-pp")
			pclFile := filepath.Join(testDir, tt.Directory+".pp")
			testDir = filepath.Join(testDir, testcase.Language)
			err = os.MkdirAll(testDir, 0700)
			if err != nil && !os.IsExist(err) {
				t.Fatalf("Failed to create %q: %s", testDir, err)
			}

			contents, err := ioutil.ReadFile(pclFile)
			if err != nil {
				t.Fatalf("could not read %v: %v", pclFile, err)
			}

			expectedFile := filepath.Join(testDir, tt.Directory+"."+testcase.Extension)
			expected, err := ioutil.ReadFile(expectedFile)
			if err != nil && !pulumiAccept {
				t.Fatalf("could not read %v: %v", expectedFile, err)
			}

			parser := syntax.NewParser()
			err = parser.ParseFile(bytes.NewReader(contents), tt.Directory+".pp")
			if err != nil {
				t.Fatalf("could not read %v: %v", pclFile, err)
			}
			if parser.Diagnostics.HasErrors() {
				t.Fatalf("failed to parse files: %v", parser.Diagnostics)
			}

			program, diags, err := pcl.BindProgram(parser.Files,
				pcl.PluginHost(utils.NewHost(testdataPath, tt.MockPluginVersions)))
			if err != nil {
				t.Fatalf("could not bind program: %v", err)
			}
			if diags.HasErrors() {
				t.Fatalf("failed to bind program: %v", diags)
			}
			var files map[string][]byte
			// generate a full project and check expected package versions
			if testcase.IsGenProject {
				project := workspace.Project{
					Name:    "test",
					Runtime: workspace.NewProjectRuntimeInfo(testcase.Language, nil),
				}
				err = testcase.GenProject(testDir, project, program)
				assert.NoError(t, err)

				depFilePath := filepath.Join(testDir, testcase.DependencyFile)
				outfilePath := filepath.Join(testDir, testcase.OutputFile)
				CheckVersion(t, tt.Directory, depFilePath, testcase.ExpectedVersion)
				GenProjectCleanUp(t, testDir, depFilePath, outfilePath)

			}
			files, diags, err = testcase.GenProgram(program)
			assert.NoError(t, err)
			if expectNYIDiags {
				var tmpDiags hcl.Diagnostics
				for _, d := range diags {
					if !strings.HasPrefix(d.Summary, "not yet implemented") {
						tmpDiags = append(tmpDiags, d)
					}
				}
				diags = tmpDiags
			}
			if diags.HasErrors() {
				t.Fatalf("failed to generate program: %v", diags)
			}

			if pulumiAccept {
				err := ioutil.WriteFile(expectedFile, files[testcase.OutputFile], 0600)
				require.NoError(t, err)
			} else {
				assert.Equal(t, string(expected), string(files[testcase.OutputFile]))
			}
			if !skipCompile && testcase.Check != nil && !tt.SkipCompile.Has(testcase.Language) {
				extraPulumiPackages := codegen.NewStringSet()
				for _, n := range program.Nodes {
					if r, isResource := n.(*pcl.Resource); isResource {
						pkg, _, _, _ := r.DecomposeToken()
						if pkg != "pulumi" {
							extraPulumiPackages.Add(pkg)
						}
					}
				}
				testcase.Check(t, expectedFile, extraPulumiPackages)
			}
		})
	}
}

// CheckVersion checks for an expected package version
// Todo: support checking multiple package expected versions
func CheckVersion(t *testing.T, dir, depFilePath string, expectedVersionMap map[string]PkgVersionInfo) {
	depFile, err := os.Open(depFilePath)
	require.NoError(t, err)

	// Splits on newlines by default.
	scanner := bufio.NewScanner(depFile)

	match := false
	expectedPkg, expectedVersion := strings.TrimSpace(expectedVersionMap[dir].Pkg),
		strings.TrimSpace(expectedVersionMap[dir].OpAndVersion)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, expectedPkg) {
			line = strings.TrimSpace(line)
			actualVersion := strings.TrimPrefix(line, expectedPkg)
			actualVersion = strings.TrimSpace(actualVersion)
			expectedVersion = strings.Trim(expectedVersion, "v:^/> ")
			actualVersion = strings.Trim(actualVersion, "v:^/> ")
			if expectedVersion == actualVersion {
				match = true
				break
			}
			actualSemver, err := semver.Make(actualVersion)
			if err == nil {
				continue
			}
			expectedSemver, _ := semver.Make(expectedVersion)
			if actualSemver.Compare(expectedSemver) >= 0 {
				match = true
				break
			}
		}
	}
	require.True(t, match)
}

func GenProjectCleanUp(t *testing.T, dir, depFilePath, outfilePath string) {
	os.Remove(depFilePath)
	os.Remove(outfilePath)
	os.Remove(dir + "/.gitignore")
	os.Remove(dir + "/Pulumi.yaml")
}
