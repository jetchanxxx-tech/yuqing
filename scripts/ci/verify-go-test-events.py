#!/usr/bin/env python3
"""Require real PostgreSQL contract runs in the complete go test -json log.

The workflow runs the whole packages below with YUQING_TEST_PG_URL set. The
explicit names prevent a renamed/deleted test, empty selector, or memory-only
contract from silently satisfying the gate. Add future accountadmin contracts
here only after their package and PostgreSQL tests exist.
"""

import argparse
import json
from pathlib import Path
import sys


MODULE = "github.com/yuqing/platform"
REQUIRED_TESTS = {
    "cmd/cli": ("TestFixedAdminBindingIsImmutableAndIdempotent",),
    "internal/app": ("TestPGServerWorkerAcrossProcessAndRestart", "TestBillingDurabilityPGReportFailureCannotCompleteOrKeepReportCharge"),
    "internal/api/v1": (
        "TestBillingEntitlementsPGUsesActualPlanBalanceAndExplicitCycle",
        "TestBillingEntitlementsPGFixedAccountShowsRealBalanceAndPendingCost",
        "TestBillingUsagePGActualCostReplayLateSettlementAndQuotaIsolation",
        "TestBillingActorPGQueuedKeyCreationRechecksOriginalJWTAndPermission",
        "TestBillingLedgerPGEnforcesBidirectionalImmutableRunConsumption",
        "TestBillingActorPGFixedOwnerKeyChargesOnlyItsImmutableCreator",
        "TestBillingActorPGKeyCreatorIsServerAssignedImmutableAndPersistent",
        "TestBillingActorPGLegacyUnknownKeyCanReadButCannotCreateCharges",
        "TestBillingActorPGKnownOwnerKeyRetainsMachinePermissionsAndRejectsDisabledOwner",

        "TestPGPublicRegistrationCannotBootstrapPlatformAdministrator",
    ),
    "migrations": (
        "TestReportCenterMigration",
        "TestReportCenterMigration/clean_v7",
        "TestReportCenterMigration/hotfixed_v7",
        "TestAccountAdminSecurityMigration",
    ),
    "internal/platform/billingpolicy": ("TestExemptionDoesNotFollowRoleTenantOrTokenEmail",),
    "internal/platform/apikey": ("TestAPIKeyStore_PG_satisfiesContract", "TestAPIKeyStore_PG_survivesNewInstance"),
    "internal/platform/auth": (
        "TestAuthStore_PG_satisfiesContract",
        "TestAuthStore_PG_survivesNewInstance",
        "TestAuthStore_PG_emailLookupIsCaseInsensitive",
        "TestAuthStore_PG_memberWithUnknownRefsIsNotFound",
        "TestAuthStore_PG_duplicateSlugOrDBNameIsConflict",
        "TestUserCenter_PG_satisfiesContract",
        "TestUserCenter_PG_survivesNewInstance",
        "TestAuthorizationSecurityPGDisabledUserCannotLoginAuthenticateOrRefresh",
        "TestAuthorizationSecurityPGAuthenticateUsesCurrentMemberRole",
        "TestAuthorizationSecurityPGLoginRecordsLastLoginAt",
        "TestAuthorizationSecurityPGRegistrationRollsBackTenantFailure",
        "TestAuthorizationSecurityPGRegistrationRollsBackMemberFailure",
        "TestPasswordChangeConcurrentPGOnlyOneOldPasswordRequestSucceeds/postgres",
        "TestPasswordChangeConcurrentPGDisabledAfterSnapshotCannotUpdate/postgres",
    ),
    "internal/platform/tenant": (
        "TestTenantStore_PG_satisfiesContract",
        "TestTenantStore_PG_survivesNewInstance",
    ),
    "internal/platform/accountadmin": (
        "TestAccountAdminPGBootstrapSerializesInitialGrant",
        "TestAccountAdminPGQueuedActorRevocationCannotCommit",
        "TestAccountAdminPGQueuedActorRevocationCannotCommit/revoke/grant",
        "TestAccountAdminPGQueuedActorRevocationCannotCommit/revoke/member",
        "TestAccountAdminPGQueuedActorRevocationCannotCommit/revoke/tenant",
        "TestAccountAdminPGQueuedActorRevocationCannotCommit/disable/grant",
        "TestAccountAdminPGQueuedActorRevocationCannotCommit/disable/member",
        "TestAccountAdminPGQueuedActorRevocationCannotCommit/disable/tenant",
        "TestAccountAdminPGQueuedActorRevocationCannotCommit/password/grant",
        "TestAccountAdminPGQueuedActorRevocationCannotCommit/password/member",
        "TestAccountAdminPGQueuedActorRevocationCannotCommit/password/tenant",
        "TestAccountAdminPGUserStatusCASRevocationAndAuditPersists",
        "TestAccountAdminPGPlatformRoleRevocationKeepsMembershipAndCannotBootstrapAgain",
        "TestAccountAdminPGConcurrentLastPlatformAdminProtection",
        "TestAccountAdminPGConcurrentLastPlatformAdminProtection/revoke",
        "TestAccountAdminPGConcurrentLastPlatformAdminProtection/disable",
        "TestAccountAdminPGConcurrentLastPlatformAdminProtection/mixed",
        "TestAccountAdminPGConcurrentLastTenantAdminProtection",
        "TestAccountAdminPGMemberRoleScopeCASRevocationAndAudit",
        "TestAccountAdminPGAuditFailureRollsBackMutationAndVersions",
        "TestAccountAdminPGAuditFailureRollsBackMutationAndVersions/user",
        "TestAccountAdminPGAuditFailureRollsBackMutationAndVersions/member",
        "TestAccountAdminPGAuditFailureRollsBackMutationAndVersions/tenant",
        "TestAccountAdminPGTenantPaginationCountsPlanAndStatusCAS",
        "TestAccountAdminPGUsersPaginationPrivacyAndReadFilters",
        "TestAccountAdminPGAdminPermissionsExcludeMemberRolesAndAPIKeys",
    ),
    "internal/platform/credit": ("TestService_PG_satisfiesContract",),
    "internal/platform/payment": ("TestPGStore_contract",),
    "internal/business/analysis": (
        "TestK4PGRerunUsesCurrentActorAndPreservesOriginalCreator",
        "TestK4PGConcurrentRerunCreatesOnlyOneNewCharge",
        "TestK4PGAdmissionRechecksQueuedJWTVersion",
        "TestK4PGStaleWorkerCannotWriteIntoNewRerun",
        "TestK4PGTaskMessageContainsPersistedRunIdentity",
        "TestK4PGCreateConsumesOneCreditAndPublishesOneTask",
        "TestK4PGZeroCreditsRejectsWithoutTaskOrDebit",
        "TestK4PGRerunConsumesAnotherCreditAndPublishesOneTask",
        "TestK4PGRerunWithoutCreditsPreservesCompletedResult",
        "TestK4PGCreateQueueFailureRollsBackCreditAndAnalysis",
        "TestK4PGRerunQueueFailurePreservesCreditAndPreviousOutput",
        "TestK4PGConcurrentCreateCannotOversellPurchasedCredits",
        "TestK4PGLegacyBetaFlagCannotTransferFixedAdminExemption",
        "TestK4PGFailureOfUnchargedRerunCannotRefundPriorSuccessfulConsume",
        "TestK4PGFailureRefundsOnlyLatestPaidExecutionOnce",
        "TestK4PGRefundFailureCannotCommitTerminalState",

        "TestPGCreateAndRerunPublishAtomically",
        "TestPGCreateWithoutCreditsRejectsAtomically",
        "TestPGCanceledTaskRejectsDocumentsAndDatabaseErrorsFailPipeline",
        "TestPGStore_filterSnapshotAndLegacyDefaults",
        "TestPGStore_idIsGlobalPrimaryKey",
        "TestPGStore_listOmitsReportContent",
        "TestPGDocumentStore_publishedAtNormalizedToUTC",
        "TestPGDocumentStore_publishedAtNonStandardDropped",
        "TestPGDocumentStore_duplicateIDStoredOnce",
        "TestPGDocumentStore_emptyIDGetsGenerated",
        "TestServiceWithPGStore_survivesServiceRebuild",
        "TestAnalysisStore_putGetRoundTrip/postgres",
        "TestAnalysisStore_putPreservesNilAndEmptySlices/postgres",
        "TestAnalysisStore_putDuplicateConflicts/postgres",
        "TestAnalysisStore_getNotFoundAndTenantIsolation/postgres",
        "TestAnalysisStore_listSortsAndScopesByTenant/postgres",
        "TestAnalysisStore_mutateAppliesAndPersists/postgres",
        "TestAnalysisStore_mutateSerializesConcurrentUpdates/postgres",
        "TestDocumentStore_addListCount/postgres",
        "TestDocumentStore_emptyReturnsEmptySliceAndZero/postgres",
        "TestDocumentStore_isolatedByTenantAndAnalysis/postgres",
        "TestDocumentStore_publishedAtRoundTrip/postgres",
    ),
    "internal/business/report": (
        "TestPGReportStore_requiresExistingCreator",
        "TestPGReportCreateOnceRedeliveryAndRerun",
        "TestPGReportStore_titleAndSummaryNotPersisted",
        "TestReportStore_createGetRoundTrip/postgres",
        "TestReportStore_createDuplicateConflicts/postgres",
        "TestReportStore_listFiltersAndPaginates/postgres",
        "TestReportStore_updateStatus/postgres",
    ),
    "internal/business/alert": (
        "TestAlertStore_PG_satisfiesContract",
        "TestAlertStore_PG_listRejectsForeignTenant",
        "TestAlertStore_PG_createdAtIsNotPersisted",
        "TestAlertStore_PG_survivesNewInstance",
        "TestServiceOverPGStore_checkFiresAndPersistsTrigger",
    ),
}


def verify_events(path: Path, go_exit_code: int) -> list[str]:
    errors = []
    started_packages = set()
    passed_packages = set()
    started_tests = set()
    passed_tests = set()
    event_count = 0

    if go_exit_code != 0:
        errors.append(f"go test exited with status {go_exit_code}")

    with path.open(encoding="utf-8") as stream:
        for line_number, line in enumerate(stream, 1):
            if not line.strip():
                continue
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                errors.append(f"line {line_number}: not a go test JSON event")
                continue
            if not isinstance(event, dict) or not isinstance(event.get("Action"), str):
                errors.append(f"line {line_number}: missing event action")
                continue
            event_count += 1
            action = event["Action"]
            package = event.get("Package", event.get("ImportPath", ""))
            test = event.get("Test", "")
            if not isinstance(package, str) or not isinstance(test, str):
                errors.append(f"line {line_number}: invalid package or test name")
                continue
            label = f"{package}/{test}" if test else package

            if action in {"skip", "fail", "build-fail"}:
                errors.append(f"{action}: {label}")
            if action == "start" and package:
                started_packages.add(package)
            elif action == "run" and test:
                key = (package, test)
                if key in started_tests:
                    errors.append(f"test ran more than once: {label}")
                started_tests.add(key)
            elif action == "pass":
                if test:
                    key = (package, test)
                    if key not in started_tests:
                        errors.append(f"pass without run: {label}")
                    passed_tests.add(key)
                else:
                    passed_packages.add(package)

    if event_count == 0:
        errors.append("test event log is empty")
    for package_path, tests in REQUIRED_TESTS.items():
        package = f"{MODULE}/{package_path}"
        if package not in started_packages or package not in passed_packages:
            errors.append(f"package must start and pass: {package}")
        for test in tests:
            key = (package, test)
            if key not in started_tests or key not in passed_tests:
                errors.append(f"required PG test must run and pass: {package}/{test}")
    for package, test in sorted(started_tests - passed_tests):
        errors.append(f"test did not finish with pass: {package}/{test}")
    for package in sorted(started_packages - passed_packages):
        errors.append(f"package did not finish with pass: {package}")
    return errors


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("event_log", type=Path)
    parser.add_argument("--go-exit-code", type=int, required=True)
    args = parser.parse_args()
    try:
        errors = verify_events(args.event_log, args.go_exit_code)
    except (OSError, UnicodeError) as error:
        print(f"ERROR: cannot read complete test events: {error}", file=sys.stderr)
        return 1
    if errors:
        for error in errors:
            print(f"ERROR: {error}", file=sys.stderr)
        return 1
    required_count = sum(len(tests) for tests in REQUIRED_TESTS.values())
    print(
        f"Verified {required_count} required PostgreSQL test runs in "
        f"{len(REQUIRED_TESTS)} packages; no skipped or failed events."
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
