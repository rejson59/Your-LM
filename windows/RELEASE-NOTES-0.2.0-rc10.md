# YourLM 0.2.0-rc10 — większy margines dla Androida po diagnozie RC9

RC9 zrobił to, po co powstał: zachował na Windows ostatnią uwierzytelnioną migawkę Redmi tuż przed zniknięciem całej aplikacji. Migawka wykazała tylko **614 088 704 B dostępnego RAM** podczas rozpoczynania transferu tensora **207,97 MiB**, a historia Androida zawiera powtarzające się zakończenia `LOW_MEMORY`. Nie było kontrolowanego zakończenia procesu natywnego. Najsilniejszą diagnozą jest więc zabicie aplikacji/UID przez Androida podczas przejściowego szczytu pamięci przy ładowaniu dużego tensora.

## Zmiany

- Host odczytuje z GGUF rozmiar największego zakodowanego tensora i wylicza `--fit-target` jako bazowe 1024 MiB plus cztery jego rozmiary, zaokrąglone do 256 MiB i z minimum **2048 MiB**. Dla badanego tensora 207,97 MiB daje to 2048 MiB zamiast 1024 MiB. Oznacza to nieco mniej warstw na telefonie, ale obejmuje docelowy bufor tensora, transport, brudne strony cache, bookkeeping backendu i nagłą presję Androida.
- Windows opisuje ten margines spójnie w panelu pamięci, stanie serwera i raporcie po awarii.
- Wspólny raport Windows/telefon dodaje krótką ocenę presji pamięci, jeśli ostatnia migawka ma mniej niż 1 GiB dostępnego RAM, zgłasza `memoryLow` albo zawiera historię `LOW_MEMORY`.
- Telemetria workera pobiera PID bezpośrednio z uruchomionego procesu (z dotychczasowym skanem `/proc` jako fallback), raportuje RSS, PSS, szczyt RSS, pamięć anonimową/plikową/współdzieloną i swap natywnego RPC oraz osobno pamięć aplikacji Java. Pole łącznego obciążenia używa PSS, gdy Android udostępnia `smaps_rollup`, zamiast przedstawiać samo chwilowe `rpcResidentBytes` jako cały nacisk pamięci.
- Rutynowe `Socket closed` z nici odpowiedzi krótkiego połączenia nie jest już zapisywane jako awaria przekazywania RPC. `Broken pipe` i `Connection reset` pozostają widoczne, bo mogą wskazywać rzeczywistą utratę workera.

## Bez zmian

Pozostają automatyczne migawki diagnostyczne, jeden kopiowalny raport hosta i telefonu, trwały cache RPC, rzeczywiste etapy ładowania z czasem, wskazówki, zatrzymanie, tryb ciemny, instalacja per-user, ochrona przed downgrade i skrót w Menu Start. RC10 nie wymusza globalnie `--load-mode none` i nie pokazuje wymyślonego procentu postępu.
