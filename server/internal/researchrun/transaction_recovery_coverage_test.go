package researchrun

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestResearchTransactionRecoveryMatrixCoversRegistry(t *testing.T) {
	t.Helper()
	registry := parseResearchTransactionOperationRegistry(t)
	covered := parseRecoveryMatrixOperations(t, registry)
	bespoke := map[researchTxOperation]string{
		txOpDispatchIntentCreate:       "TestCreateDispatchIntentTransactionRecovery",
		txOpInquiryStatusUpdate:        "TestUpdateInquiryStatusTransactionRecovery",
		txOpRunSelectiveSteer:          "TestApplySelectiveSteeringTransactionRecovery",
		txOpStrategyPromotion:          "TestPersistStrategyPromotionTransactionRecovery",
		txOpTaskInquiryTargetsBind:     "TestBindTaskInquiryTargetsTransactionRecovery",
		txOpSearchLineageRecord:        "TestRecordSearchLineageBatchTransactionRecovery",
		txOpV6TeamMemberAdd:            "TestV6TeamCapacityRules",
		txOpV6TeamMemberArchive:        "TestV6TeamArchiveRequiresReason",
		txOpV6DirectorAssign:           "TestV6DirectorAssignmentValidation",
		txOpV6DirectorUnavailable:      "TestV6DirectorFailureValidation",
		txOpV6DirectorCycleCreate:      "TestV6DirectorBriefIsBoundedAndPaged",
		txOpV6DirectorBriefAck:         "TestV6DirectorBriefAcknowledgementDelegates",
		txOpV6SubmissionApply:          "TestV6SubmissionApplicationTransactionBoundary",
		txOpV6DiscussionOpen:           "TestV6DiscussionPersistenceContract",
		txOpV6MatchDecisionRecord:      "TestV6MatchDecisionPersistenceContract",
		txOpV6DispatchPrepare:          "TestV6DispatchPreparationTransactionBoundary",
		txOpV6DispatchComplete:         "TestV6DispatchCompletionTransactionBoundary",
		txOpV6SteeringApply:            "TestV6SteeringAssessmentTransactionBoundary",
		txOpV6SteeringTriggerClaim:     "TestV6SteeringTriggerTransactionBoundary",
		txOpV6DirectorProposalClaim:    "TestV6DirectorProposalClaimTransactionBoundary",
		txOpV6DirectorProposalComplete: "TestV6DirectorProposalCompletionTransactionBoundary",
		txOpV6ReportUploadCreate:       "TestV6ReportUploadCreateTransactionBoundary",
		txOpV6ReportUploadComplete:     "TestV6ReportUploadCompleteTransactionBoundary",
		txOpV6ReportPackageClaim:       "TestV6ReportPackageClaimTransactionBoundary",
		txOpV6ReportPackageAccept:      "TestV6ReportPackageAcceptTransactionBoundary",
		txOpV6ReportReview:             "TestV6ReportReviewTransactionBoundary",
		txOpV6ReportWorkCreate:         "TestV6ReportWorkCreateTransactionBoundary",
		txOpV6ProjectionSnapshot:       "TestV6ProjectionSnapshotTransactionBoundary",
		txOpV6ProjectionSlice:          "TestV6ProjectionSliceTransactionBoundary",
		txOpV6OutboxReschedule:         "TestV6OutboxSQLGuardsLeaseAndEmitsFailureEvent",
		txOpV6OutboxFail:               "TestV6OutboxSQLGuardsLeaseAndEmitsFailureEvent",
	}
	for operation, testName := range bespoke {
		covered[operation] = struct{}{}
		if !testFunctionExists(t, testName) {
			t.Fatalf("bespoke recovery test %s for %s is missing", testName, operation)
		}
	}
	for name, operation := range registry {
		if _, ok := covered[operation]; !ok {
			t.Fatalf("missing recovery matrix row for %s (%s)", name, operation)
		}
	}
	for operation := range covered {
		found := false
		for _, registered := range registry {
			if registered == operation {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("recovery matrix covers unregistered operation %s", operation)
		}
	}
}

func parseResearchTransactionOperationRegistry(t *testing.T) map[string]researchTxOperation {
	t.Helper()
	registry := map[string]researchTxOperation{}
	source, err := os.ReadFile("transaction.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "transaction.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		genDecl, ok := declaration.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.CONST {
			continue
		}
		for _, spec := range genDecl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok || len(valueSpec.Names) != 1 || len(valueSpec.Values) != 1 {
				continue
			}
			typeIdent, ok := valueSpec.Type.(*ast.Ident)
			if !ok || typeIdent.Name != "researchTxOperation" {
				continue
			}
			lit, ok := valueSpec.Values[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			value, unquoteErr := strconv.Unquote(lit.Value)
			if unquoteErr != nil {
				t.Fatal(unquoteErr)
			}
			registry[valueSpec.Names[0].Name] = researchTxOperation(value)
		}
	}
	if len(registry) == 0 {
		t.Fatal("expected research transaction operation registry")
	}
	return registry
}

func parseRecoveryMatrixOperations(t *testing.T, registry map[string]researchTxOperation) map[researchTxOperation]struct{} {
	t.Helper()
	covered := map[researchTxOperation]struct{}{}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		source, readErr := os.ReadFile(entry.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), entry.Name(), source, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := ""
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				name = fun.Name
			case *ast.SelectorExpr:
				name = fun.Sel.Name
			default:
				return true
			}
			if name != "runTransactionRecoveryMatrix" {
				return true
			}
			if len(call.Args) < 2 {
				return true
			}
			ident, ok := call.Args[1].(*ast.Ident)
			if !ok {
				return true
			}
			operation, ok := registry[ident.Name]
			if !ok {
				t.Fatalf("recovery matrix references unknown operation constant %q", ident.Name)
			}
			covered[operation] = struct{}{}
			return true
		})
	}
	if len(covered) == 0 {
		t.Fatal("expected at least one recovery matrix operation")
	}
	var listed []string
	for operation := range covered {
		listed = append(listed, string(operation))
	}
	sort.Strings(listed)
	t.Logf("recovery matrix covers %d operations: %s", len(listed), strings.Join(listed, ", "))
	return covered
}

func testFunctionExists(t *testing.T, name string) bool {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		source, readErr := os.ReadFile(entry.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), entry.Name(), source, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if ok && function.Name.Name == name {
				return true
			}
		}
	}
	return false
}
