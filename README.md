# jag

## Umgebungsvariablen (`cmd/phase2check`, teilweise auch `cmd/hjsonimport`)

Der Phase-2-Testtreiber `cmd/phase2check` (Import + Container/Terminal/Circuit-Aufbau
gegen eine CIM/CGMES/NSC-Beispieldatei oder ein ganzes Verzeichnis) liest folgende
Umgebungsvariablen, um die Verarbeitung zu steuern, ohne den Code anzufassen:

| Variable | Wirkung | Default |
|---|---|---|
| `JAG_DATABASE` | Wählt Backend UND Ziel-Datenbank in einem Wert (siehe eigenen Abschnitt unten). **Muss immer gesetzt sein** — es gibt keinen Default mehr. | kein Default, Pflichtangabe |
| `JAG_FORCE_NSC` | `1` erzwingt den NSC-Dialekt-Import (`phase1.RunNSCFiles`) auch für ein Verzeichnis, das nur `.xml`- (nicht `.rdf`-)Dateien enthält, z. B. `example_as_cim.xml`. Ohne diese Variable entscheidet die Dateiendung (`.rdf`-Dateien im Verzeichnis ⇒ NSC). | unset (Endungs-Heuristik) |
| `JAG_CHUNK_SIZE` | Cursor-Batch-Größe (`staging.Store.GetByClass`-Limit) für alle klassenweisen Scans innerhalb eines Pass-A-Batches bzw. in Pass B (z. B. Substation-/Building-Paging, ACLineSegment-/Junction-Scans, die abschließenden flag-basierten Vollständigkeitsprüfungen). Größer = weniger DB-Roundtrips; seit dem Pass-A/B-Umbau ist dies **nicht** mehr der RAM-bestimmende Parameter (das ist `JAG_STATION_BATCH_SIZE`, s. u.) — nur noch eine reine DB-Roundtrip-Effizienz-Stellschraube. | `2000` |
| `JAG_STATION_BATCH_SIZE` | Anzahl Substation-/Building-Wurzeln pro Pass-A-Batch (`common.RunPassA`). Dies ist der eigentliche RAM-Begrenzer der Pipeline: der Node-/Edge-/Attribut-/Geometrie-Fußabdruck eines Batches skaliert mit dieser Zahl, nicht mit der Gesamtmodellgröße. | `1000` (`common.DefaultStationBatchSize`) |
| `JAG_STATION_WORKERS` | Anzahl paralleler Pull-Pool-Worker-Goroutinen in Pass A (`common.RunPassA`) — jeder Worker verarbeitet nacheinander ganze Batches (siehe `JAG_STATION_BATCH_SIZE`) über `ProcessStationBatch`. | `4` (`common.DefaultPassAWorkers`) |
| `JAG_PASS_B_WORKERS` | Anzahl paralleler Worker-Goroutinen für Pass B's ACLineSegment-Ketten-Build-Schritt (`common.RunPassB`/`discoverACLineChainsStreaming`). Die Ermittlung der Ketten-Zugehörigkeit selbst läuft bewusst einzelsträngig (Korrektheit hat Vorrang, siehe Kommentar in `acline_streaming.go`); nur der reine CPU-Build-Schritt pro bereits ermittelter Kette (Container-ID, Name, Node-/Edge-Aufbau) wird parallelisiert. Deckungsgleich mit `JAG_STATION_WORKERS`'s Default gehalten. | `4` (`common.DefaultPassBWorkers` = `common.DefaultPassAWorkers`) |
| `JAG_PASS_B_BATCH_SIZE` | Analog zu `JAG_STATION_BATCH_SIZE`, aber für Pass B: Anzahl bereits ermittelter ACLineSegment-Ketten (physische Kabeltrassen), die in einem Batch gebaut, persistiert und wieder verworfen werden (`common.RunPassB`/`discoverACLineChainsStreaming`'s Batch-Modus). Ein Lasttest (lasttest-500, 2026-07-18/19) zeigte, dass Pass B's RAM-Spitze mit der Gesamtzahl seiner Gruppen/Container skaliert, unabhängig von `JAG_STATION_BATCH_SIZE` (Pass B las diese Variable nie) — dieser eigene Batch-Größen-Regler ist die Behebung dafür. | `1000` (`common.DefaultPassBBatchSize` = `common.DefaultStationBatchSize`) |
| `JAG_CPU_PROFILE` | Pfad, unter dem ein `pprof`-CPU-Profil des gesamten Laufs geschrieben wird. | unset (kein Profil) |
| `JAG_KEEP_STAGING` | `1` überspringt das automatische Aufräumen von `staging_records`/`staging_errors` (und `import_flag`), das sonst nach einem erfolgreich abgeschlossenen Import läuft (`common.FinalizeImport`) — Staging ist reine, versionsscoped Phase-1-Zwischenablage, die Phase 2/3 nach erfolgreichem Lauf nicht mehr braucht. Setzen, falls dieselbe Datenbank auch mit `internal/jag2nsc`s Postgres-only `NSC_SUPPORT`-Feature (`BuildTopology`/`BuildNetworkGroup`/`BuildCircuits`) genutzt wird, das `staging_records` direkt liest. | unset (Staging wird nach Erfolg gelöscht) |
| `JAG_SKIP_VACUUM` | `1` überspringt das automatische `VACUUM`, das sonst nach einem erfolgreich abgeschlossenen Import per Default läuft (`common.FinalizeImport`/`staging.Store.Vacuum`) — SQLite/Postgres geben durch `DeleteVersion` bzw. Pass A/B's Delete-dann-Insert-Re-Upsert-Muster frei gewordene Seiten nie ans Dateisystem zurück, sondern verwalten sie intern in einer Freelist; ohne `VACUUM` besteht die Datenbankdatei dadurch zu einem großen Teil (am `lasttest-200`-Datensatz gemessen: ~76 %) aus ungenutzten Freelist-Seiten. `VACUUM` schreibt dafür die gesamte Datei neu (Laufzeit-/temporärer-Speicherplatz-Kosten) — bei sehr großen Datenbanken ggf. bewusst überspringen und separat/später laufen lassen. | unset (VACUUM läuft nach Erfolg) |
| `JAG_IMPORT_LABEL` | Optionales, frei wählbares Label, das nach einem erfolgreich abgeschlossenen Import zusammen mit dem globalen Metadata-Datensatz (`pkg/core/metadata`, ein einzelner, bei jedem erfolgreichen Import überschriebener Datensatz mit `Number`/`Timestamp`/`Label`) gespeichert wird (`metadata.Store.Record`). Bleibt es leer, wird automatisch `"v"+Number` verwendet (siehe `pkg/core/metadata`'s Doc-Kommentar). | unset (Default-Label `"v"+Number`) |

**`cmd/hjsonimport`/`cmd/hjsonwatch`/`cmd/hjsonimport-deprecated`** (die Fachmodell-HJSON-Treiber,
siehe Konzept.md's "HJSON Fachmodell"-Abschnitt) lesen dieselben acht Variablen `JAG_DATABASE`,
`JAG_CHUNK_SIZE`, `JAG_STATION_BATCH_SIZE`, `JAG_STATION_WORKERS`, `JAG_PASS_B_WORKERS`,
`JAG_PASS_B_BATCH_SIZE`, `JAG_KEEP_STAGING`, `JAG_SKIP_VACUUM` und `JAG_IMPORT_LABEL` mit
identischen Defaults — `JAG_FORCE_NSC` (HJSON hat keine CIM/CGMES/NSC-Dialekterkennung) und
`JAG_CPU_PROFILE` (kein CPU-Profiling) gelten dort nicht. Diese drei HJSON-Treiber unterstützen
inzwischen sowohl das SQLite- als auch das PostgreSQL-Backend (`JAG_DATABASE` mit `sqlite://`
oder `postgres://`-Präfix, siehe `pkg/jagstore`). `cmd/hjsonexport` liest keine
`JAG_*`-Variablen; es wird ausschließlich über Positionsargumente (`<db-path> <output-root>
[default-netzregion]`) gesteuert.

## `JAG_DATABASE` — einheitliche Datenbank-Konfiguration (SQLite und PostgreSQL)

Statt getrennter Variablen für Backend-Auswahl (`JAG_BACKEND`), SQLite-Pfad (`JAG_DB_PATH`)
und PostgreSQL-Verbindung (`JAG_POSTGRES_DSN`/`JAG_POSTGRES_HOST`/`JAG_POSTGRES_PORT`/
`JAG_POSTGRES_USER`/`JAG_POSTGRES_PASSWORD`/`JAG_POSTGRES_DB`/`JAG_POSTGRES_SSLMODE` — alle
inzwischen entfernt) gibt es **eine einzige Variable**, `JAG_DATABASE`, deren Wert per Präfix
sowohl das Backend als auch die Verbindungsdetails bestimmt. Geparst wird sie durch das
öffentliche Package `pkg/jagdb` (`jagdb.FromEnv()`/`jagdb.Parse(...)`), das auch von externen
Go-Projekten importiert werden kann.

| `JAG_DATABASE`-Wert | Bedeutung |
|---|---|
| `sqlite://foo.db` | SQLite-Backend; Präfix `sqlite://` wird abgeschnitten, Rest (`foo.db`) ist der Dateipfad, unverändert an `sqlite.Open` übergeben. |
| `sqlite://:memory` (ohne abschließenden Doppelpunkt!) | SQLite-Backend, In-Memory-Sonderfall; wird intern auf SQLites eigenen Marker `:memory:` gemappt. |
| `postgres://user:pass@host:port/db?sslmode=disable` (auch `postgresql://...`) | PostgreSQL-Backend; der komplette Wert wird unverändert als DSN an `postgres.Open` übergeben. |
| nicht gesetzt, leer, oder unbekanntes Präfix (z. B. `foo.db`, `mysql://...`) | Fehler/Abbruch — es gibt bewusst keinen impliziten Default mehr. |

Beispiele:

```
JAG_DATABASE=sqlite://phase2check.db go run ./cmd/phase2check examples/cgmes/BaseCase_Complete

docker run -d --name jag-pg -e POSTGRES_USER=jag -e POSTGRES_PASSWORD=jag -e POSTGRES_DB=jag -p 5432:5432 postgres:16-alpine
JAG_DATABASE=postgres://jag:jag@localhost:5432/jag?sslmode=disable go run ./cmd/phase2check examples/cgmes/BaseCase_Complete
```

Das PostgreSQL-Schema selbst wird immer im Standardschema `public` angelegt (`CREATE TABLE
IF NOT EXISTS ...` ohne Schema-Qualifizierung) - JAG legt kein eigenes Schema an und bietet
dafür bewusst keine eigene Umgebungsvariable an; wer die Tabellen in einem anderen Schema
haben möchte, steuert das server-/rollenseitig über den `search_path` (z. B. via
`JAG_DATABASE`'s `search_path`-Query-Parameter im `postgres://`-Wert).

**`cmd/sqlite2postgres`/`cmd/postgres2sqlite`** (Migrationswerkzeuge zwischen den Backends)
nehmen die SQLite-Seite weiterhin über den `-sqlite`-Flag entgegen; `JAG_DATABASE` steuert bei
ihnen ausschließlich die PostgreSQL-Gegenseite und muss dort zwingend ein `postgres://`-Wert
sein (ein `sqlite://`-Wert wird abgelehnt, da die SQLite-Seite schon über `-sqlite` feststeht).


**Hinweis (aktueller Implementierungsstand)**: Phase 2/3 laufen seit dem Pass-A/B-Umbau
(siehe `spec/Konzept.md`, Abschnitt "Pass A/B: Batch-weise Phase-2/3-Pipeline") nicht mehr als
einzelne whole-model-Schritte, sondern batch-weise über `common.RunPassA` (Stationen) gefolgt
von `common.RunPassB` (stationsübergreifende ACLineSegment-/Junction-Ketten) und einer
abschließenden, paged flag-basierten Vollständigkeitsprüfung (`common.CheckInvariantsFlagged`).
Frühere Umgebungsvariablen `JAG_TERMINAL_WORKERS`, `JAG_STATION_WORKERS` (alte Bedeutung:
Sachdaten+Geometrie-Worker), `JAG_DISABLE_ANHAENGSEL` und `JAG_SACHDATEN_SAMPLE` existieren im
aktuellen Code nicht mehr (die whole-model-Funktionen, die sie steuerten, werden von
`cmd/phase2check` nicht mehr aufgerufen) und wurden aus dieser Tabelle entfernt.

