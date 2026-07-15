# jag

## Umgebungsvariablen (`cmd/phase2check`)

Der Phase-2-Testtreiber `cmd/phase2check` (Import + Container/Terminal/Circuit-Aufbau
gegen eine CIM/CGMES/NSC-Beispieldatei oder ein ganzes Verzeichnis) liest folgende
Umgebungsvariablen, um die Verarbeitung zu steuern, ohne den Code anzufassen:

| Variable | Wirkung | Default |
|---|---|---|
| `JAG_DB_PATH` | Pfad der SQLite-Datei, in die importiert wird (bewusst eine echte Datei, nicht `:memory:`, damit Timings echte Disk-I/O widerspiegeln). Wird bei jedem Lauf vorher gelöscht (frischer Import). | `phase2check.db` |
| `JAG_FORCE_NSC` | `1` erzwingt den NSC-Dialekt-Import (`phase1.RunNSCFiles`) auch für ein Verzeichnis, das nur `.xml`- (nicht `.rdf`-)Dateien enthält, z. B. `example_as_cim.xml`. Ohne diese Variable entscheidet die Dateiendung. | unset (Endungs-Heuristik) |
| `JAG_CHUNK_SIZE` | Cursor-Batch-Größe (`staging.Store.GetByClass`-Limit) für alle klassenweisen Scans (`BuildContainers`, `ResolveTerminals`, ConnectivityNode-Referenzcheck). Größer = weniger DB-Roundtrips, aber mehr RAM pro Batch (jeder geholte Datensatz + seine aufgelösten Referenzen liegen währenddessen im Speicher); kleiner = mehr Roundtrips, aber niedrigerer RAM-Peak. | `1000` |
| `JAG_TERMINAL_WORKERS` | Worker-Zahl für die parallelen Klassen-Scans in `common.ResolveTerminalsParallel` ("Schritt (a)" der Parallel-Import-Entscheidung). Pendant zu `JAG_STATION_WORKERS` für die andere parallele Phase. | `8` (`common.DefaultTerminalScanWorkers`) |
| `JAG_STATION_WORKERS` | Worker-Zahl (Stationen pro Goroutine) für `common.BuildSachdatenAndGeometryParallel` ("Schritt (b)", Sachdaten+Geometrie-Aufbau). | `common.DefaultStationWorkers` |
| `JAG_DISABLE_ANHAENGSEL` | `1` deaktiviert den Sachdaten-Satelliten-("Anhängsel"-)Walk vollständig — nur die literalen Equipment-Attribute werden noch emittiert. Reines Diagnose-Werkzeug, um die Sachdaten-Phase-Baseline ohne Hub-Risiko zu messen. | unset (Satelliten-Walk aktiv) |
| `JAG_SACHDATEN_SAMPLE` | Beschränkt `BuildAttributes` auf die ersten N (sortierten) Equipment-IDs. Reines Diagnose-Werkzeug, um z. B. ein CPU-Profil des Sachdaten-Walks in überschaubarer Zeit gegen einen großen Datensatz aufzunehmen. | unset (alle Equipment) |
| `JAG_CPU_PROFILE` | Pfad, unter dem ein `pprof`-CPU-Profil des gesamten Laufs geschrieben wird. | unset (kein Profil) |
