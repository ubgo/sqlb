---
ds:
  covers: [promise-gate-a57aksdv]
---

# Initial promise audit

This is the bounded audit performed for the Lore, Docsync and spec-promises integration on 2026-11-01, based on SQLB main at `a3272d5` plus this change. It records evidence for this run; future changes require another review and test run.

## Scope and method

The [inventory](../testdata/promises.txt) covers public Markdown, including the README, security policy, engine reference tables and contributor guidance, plus the actual `sqlb vet -help` output. The [gate](ds:block?id=promise-gate-a57aksdv) discovers newly added public Markdown rather than relying solely on a fixed list. Repeated claims share a behavior id; non-behavior prose has an explicit classification reason. Tags link to asserting tests, but the checker cannot prove that a test is sufficient or that an integration test executed.

The sweep exercised the public Go entrypoints, error paths, repeated builds, independent schema ordering, driver execution, and the built CLI. A separately compiled consumer module queried the third-party Chinook SQLite fixture (upstream Git blob `299610dc69f04a2b16b3a2e45bd2af82ed2872a2`): the AC/DC join returned its two album rows; hostile and empty names returned zero rows with bound arguments, and the Artist table still contained 275 rows. The fixture was used only for this audit and is not vendored.

## Corrections

| Previous claim | Corrected boundary |
|---|---|
| Values do not reach SQL text, with no qualification | Ordinary query-mode parameters are bound; explicit literal/inline APIs and DDL have separate contracts |
| Rendering cannot panic for any caller | Built-in validation returns errors; arbitrary caller implementations and callbacks are outside a panic-recovery guarantee |
| An integer database column mapped to a Go string fails scanning | `database/sql` performs supported conversions; incompatible conversions fail at scan time |
| LAG with a default cannot be NULL | Nullable input or default can produce SQL NULL |
| Pagination count agrees with every page | The count is present on returned rows; an empty page contains no total |
| JSON wrapper applies in every outcome | Matched results are wrapped; empty/error handling follows JSON_QUERY options |
| Websearch accepts every configuration as well as input | Malformed query text is accepted; the text-search configuration must exist |

These were documentation overclaims, not changes to the public runtime API. The shared onboarding also had a broken private status link and stale CI/MySQL descriptions, which are corrected.

## Verification and limits

The module checks, 93 negative compile assertions, zero-dependency/reuse/task gates, Markdown checks, 112 behavior groups across 302 inventory quotes, and 11 checker tests passed. Core and SQLite race tests passed; live PostgreSQL/MySQL driver and migration race suites passed. The final combined `task check` passed with PostgreSQL 18 and MySQL DSNs, including all five Docsync references. After the user explicitly delegated the two reviewed acknowledgments, they were recorded as agent actions by `codex-cli`, delegated by `ekuplu`, in `.ds/acks.tsv`. PostgreSQL 17 and 18 and MySQL 8.4 were exercised in disposable local containers. PostgreSQL extension groups requiring `vector` and `postgis` remain unverified on those images; PostgreSQL 18-only cases skip on 17 and execute on 18. MySQL's existing default-collation case is covered by its separate case/accent behavior test. ClickHouse execution remains outside this run.

The checker negative tests reject unlisted/reworded promises, missing or misspelled tags, missing files, an empty inventory and tags found only under dependencies. Fourteen temporary production mutations were detected by the new or strengthened assertions: dropped bound arguments, partial failed queries, a core dependency, a core connection opener, a core transaction opener, cookbook coupling, a missing conflict guard, silent missing-DSN handling, early type rejection, unstable schema order, a strict search parser, missing JSON wrapper, missing CASCADE and an incorrectly generic constraint drop. Each source file was restored from an exact byte backup. No mutation is part of this patch.

Lore hydration from committed JSON was checked in a fresh repository without a local database; five rules were restored and `lore doctor` reported healthy. A temporary change to a cited test triggered a Docsync drift error, and its source was restored. The two initial review findings were resolved through the delegated acknowledgments; no diagnostics were suppressed.

## Behavior groups

The table identifies the asserting tests for each group. “Held” means those assertions passed in the recorded local scope, not a proof over all possible inputs. “Partly unverified” identifies missing server capabilities. The inventory retains the exact sentences behind each id.

| Group | Result in this run | Asserting tests |
|---|---|---|
| `builder-immutability` | Held in tested scope | `TestBuilders_MethodsDoNotWriteToTheirReceiver`; `TestWriteBuilders_MethodsDoNotWriteToTheirReceiver` |
| `capability-interfaces` | Held in tested scope | `TestClickHouse_DeclinesReturning`; `TestQuoteLiteral_ClickHouseDeclines`; `TestReturning_RefusesADialectThatSaysItHasNone` |
| `catalogue-star` | Held in tested scope | `Example_s1_1_maxOverAll` |
| `closed-sets` | Held in tested scope | `TestClosedSets_RefuseAValueOutsideTheirConstants` |
| `concat-param-type` | Held in tested scope | `TestConcat_TypesABareParameterWhereTheDialectAsks` |
| `conformance-reset` | Held in tested scope | `TestConformance` |
| `connection-ownership` | Held in tested scope | `TestPromise_CoreDoesNotOpenConnections` |
| `cross-package` | Held in tested scope | `TestPromise_CookbookPackageBoundaries`; `TestSelect_JoinRendersOnClause` |
| `ddl-authored` | Held in tested scope | `TestCreateTable_OneDeclarationBothEngines` |
| `ddl-column-validation` | Held in tested scope | `TestCreateTable_Refusals`; `TestColumnDef_RefusesAColumnWithNoName` |
| `ddl-describe` | Held in tested scope | `TestDescribe_FailsWhereBuildWould`; `TestDescribeColumn_FailsWhereTheDefinitionCannotBeWritten` |
| `ddl-empty-oneof` | Held in tested scope | `TestCreateTable_Refusals` |
| `ddl-extra-columns` | Held in tested scope | `TestWriteColumnHelpers_WriteTheClausesTheyAccept`; `TestWriteColumnHelpers_RefuseAMistargetedClause` |
| `ddl-generated-default` | Held in tested scope | `TestTable_Refusals`; `TestColumnClauses_RenderInTheirPart` |
| `ddl-guarded-constraint` | Held in tested scope | `TestGuarded_CatchesTheConditionEachKindRaises` |
| `ddl-inline` | Held in tested scope | `TestInlineArgs_NextWritesALiteral` |
| `ddl-json-default` | Held in tested scope | `TestDefault_JSONColumn` |
| `ddl-ordering` | Held in tested scope | `TestOptionRank_RankedOptionsRenderInRankOrder`; `TestOptionRank_UnrankedOptionsStayWhereTheCallerPutThem`; `TestDDL_PartitioningIsWrittenLastExecutes` |
| `ddl-standalone` | Held in tested scope | `TestAlterTable_EngineClausesStandAloneWhenTheySay` |
| `ddl-table-validation` | Held in tested scope | `TestAlterTable_RefusesAnEngineActionOnAnotherTable` |
| `engine-guard` | Held in tested scope | `TestGuards_FailUnderSQLite`; `TestMySQL_FailsUnderAnotherDialect`; `TestArgs_UnsupportedNamesConstructAndDialect` |
| `expr-boundary` | Held in tested scope | `TestExpr_ImportsOnlyRender` |
| `external-extensions` | Held in tested scope | `TestExternalConstructs_RenderAndGuard` |
| `fold-at-engine` | Held in tested scope | `TestFold_RendersPerDialect` |
| `group-parentheses` | Held in tested scope | `TestGroup_AlwaysParenthesised` |
| `insert-row-width` | Held in tested scope | `TestWriteBuilders_FailClosed` |
| `literal-explicit` | Held in tested scope | `TestArgs_LiteralIgnoresTheMode` |
| `literal-query-unguarded` | Held in tested scope | `TestQuery_LiteralHasNoGuard` |
| `literal-refusal` | Held in tested scope | `TestQuoteLiteral_RefusesWhatItCannotWriteFaithfully`; `TestQuoteLiteral_ClickHouseDeclines` |
| `literal-typestate` | Held in tested scope | `TestTypestate_LiteralHoleIsRejectedAtBuild` |
| `migration-ledger` | Held in tested scope | `TestLedger_SQLite`; `TestLedger_Postgres`; `TestLedger_MySQL`; `TestApplied_Refusals` |
| `migration-lock` | Held in tested scope | `TestLedger_SQLiteWaitsForTheWriteLockInsteadOfFailingAnUpgrade`; `TestTableLock_WaitsTimesOutAndTakesOverAStaleRow` |
| `migration-pinning` | Held in tested scope | `TestExecute_PinningIsWhatMakesAConnectionSettingHold` |
| `mysql-alter-characteristic` | Held in tested scope | `TestGaps_ClosedSetsAreRefused` |
| `mysql-backslash` | Held in tested scope | `TestQuoteLiteral_MySQL` |
| `mysql-closed-sets` | Held in tested scope | `TestClosedStringSets_RefuseAHostileValue` |
| `mysql-constraint-kind` | Held in tested scope | `TestPromise_MySQLRejectsWrongConstraintKind` |
| `mysql-constraint-table` | Held in tested scope | `TestTableConstraint_MySQLRefusesAnotherTable` |
| `mysql-create-as` | Held in tested scope | `TestForms_Render`; `TestDDL_MySQLTemporaryTables` |
| `mysql-ddl-live` | Held in tested scope | `TestDDL_MySQLTablesAndKeysHold`; `TestDDL_MySQLColumnFormsAndSwap` |
| `mysql-function-mode` | Held in tested scope | `TestGaps_ClosedSetsAreRefused` |
| `mysql-incomplete` | Held in tested scope | `TestZeroValues_AreRefused` |
| `mysql-partitioning` | Held in tested scope | `TestPartitioning_RefusesWhatMySQLRefuses` |
| `mysql-routine-body` | Held in tested scope | `TestStepBlock_SeparatorRuleIsTheServers`; `TestDDL_MySQLStepBlockExecutes` |
| `mysql-schedule` | Held in tested scope | `TestGaps_ClosedSetsAreRefused` |
| `mysql-signal` | Held in tested scope | `TestGaps_ClosedSetsAreRefused` |
| `nontransactional-index` | Held in tested scope | `TestIndexes_ConcurrentlyIsNotTransactional`; `TestExecute_RunsANonTransactionalStatementAlone`; `TestDDL_PostgresConcurrentIndexOutsideATransaction` |
| `operator-shape` | Held in tested scope | `TestShape_ExprDoorsRefuseAHostileValue`; `TestShape_ExprDoorsTakeEveryRealOperator` |
| `outside-dialect` | Held in tested scope | `TestOutsideDialect_DrivesTheWholeBuilder`; `TestOutsideDialects_DriveTheRenderLayer` |
| `parameter-binding` | Held in tested scope | `TestPromise_BoundValuesStayOutOfSQL` |
| `pg-cascade-live` | Held in tested scope | `TestPromise_CascadePreservesReferencingTable` |
| `pg-check-option` | Held in tested scope | `TestDDL_PostgresViewOptions` |
| `pg-closed-sets` | Held in tested scope | `TestClosedSets_EverySiteRefusesAHostileValueAndTakesARealOne` |
| `pg-conflict-target` | Held in tested scope | `TestPromise_ConflictUpdateNeedsTarget` |
| `pg-ddl-live` | Held in tested scope | `TestDDL_PostgresKeysAndGeneratedColumnsHold`; `TestDDL_PostgresSequencesAndViews`; `TestDDL_PostgresTriggersAndPartitions` |
| `pg-domain-constraint` | Held in tested scope | `TestDomainConstraint_RefusesAnotherDomain` |
| `pg-extension-no-cascade` | Held in tested scope | `TestObjects_SchemasExtensionsAndEnums` |
| `pg-function-quote` | Held in tested scope | `TestFunction_BodyIsDollarQuotedAwayFromItself` |
| `pg-function-result` | Held in tested scope | `TestFunction_RefuseIncompleteValues` |
| `pg-identity-live` | Held in tested scope | `TestDDL_PostgresKeysAndGeneratedColumnsHold`; `TestDDL_PostgresChangesAGeneratedColumnAndAnIdentity` |
| `pg-json-wrapper` | Held in tested scope | `TestPromise_JSONWrapperWrapsMatchedResult` |
| `pg-live` | Partly unverified: unavailable PG extensions | `TestPGConstructs_Execute`; `TestPGExtensions_Execute` |
| `pg-open-identifiers` | Held in tested scope | `TestClosedSets_OpenTypesAreQuotedRatherThanValidated` |
| `pg-recursive-view` | Held in tested scope | `TestTableShape_RefuseIncompleteValues` |
| `pg-sequence-cache` | Held in tested scope | `TestObjects_Refusals` |
| `pg-skip` | Held in tested scope | `TestPromise_PostgresSuiteSkipsWithoutDSN` |
| `pg-storage-quoting` | Held in tested scope | `TestClosedSets_OpenTypesAreQuotedRatherThanValidated` |
| `pg-trigger-params` | Held in tested scope | `TestFunction_TriggerFunctionTakesNoParameters` |
| `pg-websearch` | Held in tested scope | `TestPromise_WebsearchAcceptsMalformedQueryText` |
| `pg18-live` | Held in tested scope | `TestDDL_PG18Executes` |
| `query-overlap` | Held in tested scope | `TestQuery_RefusesOverlappingExecution`; `TestDerived_SharesTheExecutionGuard` |
| `recursive-bare` | Held in tested scope | `TestWithRecursive_UnscopedSeedIsNotParenthesised` |
| `render-boundary` | Held in tested scope | `TestRender_ImportsOnlyStdlib` |
| `render-errors` | Held in tested scope | `TestArgs_FirstErrorWins`; `TestPromise_ValidationReturnsNoPartialQuery`; `TestBitwise_FailsUnderAnotherDialect` |
| `root-zero-dependencies` | Held in tested scope | `TestPromise_CoreHasNoThirdPartyImports` |
| `scan-concurrency` | Held in tested scope | `TestDynamic_SafeAcrossGoroutines`; `TestScan_OneReaderIsSafeToShare` |
| `scan-dynamic-duplicates` | Held in tested scope | `TestDynamic_DuplicateColumnIsAnError` |
| `scan-fresh-rows` | Held in tested scope | `TestDynamic_EachMapGivesAFreshRow`; `TestDynamic_BlobsAreCopied` |
| `scan-no-columns` | Held in tested scope | `TestScan_EmptyReaderIsRejected` |
| `schema-boundary` | Held in tested scope | `TestSchema_ImportsOnlyExpr` |
| `schema-col-type` | Held in tested scope | `TestSchema_MustColPanicsOnEveryMistakeColReports` |
| `schema-composition` | Held in tested scope | `TestInstall_OrdersAcrossContributorsAndRuns`; `TestSet_OrdersTablesFromPackagesThatDoNotKnowEachOther` |
| `schema-duplicate` | Held in tested scope | `TestSet_DuplicateTableNamesBothOwners`; `TestInstall_RefusesWhatAWholeDatabaseCannotHave` |
| `schema-missing` | Held in tested scope | `TestSet_MissingReferenceIsCaughtOnAssembly`; `TestInstall_RefusesWhatAWholeDatabaseCannotHave` |
| `schema-order` | Held in tested scope | `TestSet_SortIsDeterministic`; `TestSet_CycleIsReportedWithItsMembers` |
| `select-boundaries` | Held in tested scope | `TestEverySelectBoundary_IsReachable`; `TestEveryWriteBoundary_IsReachable` |
| `sqlite-add-column` | Held in tested scope | `TestAddColumn_RefusesWhatSQLiteCannotAdd`; `TestDDL_SQLiteRefusalsMatchTheEngine` |
| `sqlite-autoincrement` | Held in tested scope | `TestDDL_SQLiteKeysStrictAndGeneratedColumnsHold` |
| `sqlite-ddl-live` | Held in tested scope | `TestDDL_SQLiteIndexesViewsTriggersAndPragmas`; `TestForms_NotNullOnConflictChangesWhatAnInsertDoes` |
| `sqlite-fk-actions` | Held in tested scope | `TestForms_DeferrabilityExecutes` |
| `sqlite-indexed-by` | Held in tested scope | `TestPlannerHints` |
| `sqlite-integrity` | Held in tested scope | `TestPragma_IntegrityChecksReturnOkWhenSound` |
| `sqlite-json-arrow` | Held in tested scope | `TestJSON1_Executes` |
| `sqlite-modifier` | Held in tested scope | `TestForms_ModifierIsQuotedAndClosedWhereItCanBe` |
| `sqlite-notnull-conflict` | Held in tested scope | `TestForms_NotNullReplaceNeedsADefault`; `TestForms_NotNullOnConflictChangesWhatAnInsertDoes` |
| `sqlite-page-size` | Held in tested scope | `TestPragma_RefusesWhatSQLiteWouldIgnore` |
| `sqlite-pragmas` | Held in tested scope | `TestPragmas_AppliedToEveryConnection`; `TestWithPragma_Overrides` |
| `sqlite-raise` | Held in tested scope | `TestForms_RaiseRefusesTheRow` |
| `sqlite-rebuild` | Held in tested scope | `TestFTS5AndRebuild_Render` |
| `sqlite-temp` | Held in tested scope | `TestForms_TemporaryRefusesAnotherSchema`; `TestForms_TemporaryQualifiedIsTheEnginesRule` |
| `sqlite-time-default` | Held in tested scope | `TestQuoteLiteral_RefusesWhatItCannotWriteFaithfully`; `TestDDL_SQLiteRefusesATimeDefault` |
| `sqlite-user-version` | Held in tested scope | `TestPragma_SetsWhatItSaysOnSQLite` |
| `stmt-boundary` | Held in tested scope | `TestStmt_ImportsOnlyTheLayersBelow`; `TestStmt_BuildsWithoutAResultStrategy` |
| `structbind-empty` | Held in tested scope | `TestNew_EmptyColumnFromACustomNamer`; `TestNamers` |
| `structbind-namer` | Held in tested scope | `TestNew_NamerIsChosenPerMapping` |
| `structbind-tag` | Held in tested scope | `TestNew_TagAlwaysBeatsTheNamer` |
| `structbind-unchecked-type` | Held in tested scope | `TestPromise_StructbindDefersColumnTypeCheck` |
| `typed-clauses` | Held in tested scope | `scripts/check-typecheck.py (every negative compile site)` |
| `vet-key-columns` | Held in tested scope | `TestCheckTables_KeysAndDefaults_SQLite` |
| `vet-schema-qualified` | Held in tested scope | `TestCompare_LooksUpTheDeclaredSchema`; `TestReadCatalogue_KeysTheDefaultSchemaBothWays` |
| `vet-source-checks` | Held in tested scope | `TestRepo_ReportsOnlyTheDeliberatePins`; `TestStatic_ReportsEachMistakeOnceAndTheRightUsesNever` |
| `zero-values` | Held in tested scope | `TestZeroValues_AreRefused`; `TestZeroValues_ReportsEachWayAZeroValueGoesWrong` |
