# YourLM 0.2.0-rc11 — odporny worker i modele z Hugging Face

To wydanie zachowuje wszystkie zabezpieczenia pamięci oraz diagnostykę RC10, a usuwa dwa duże ograniczenia pierwszego uruchomienia: terminalne wyłączanie workera po zakończeniu procesu natywnego oraz konieczność wcześniejszego ręcznego pobrania GGUF.

## Worker Android, który potrafi się odtworzyć

- Żądany stan workera jest zapisywany niezależnie od stanu Activity i usługi. `START_STICKY` może odtworzyć usługę po jej rekreacji przez Androida, ale jawne **Zatrzymaj worker** natychmiast zapisuje stan wyłączony i nigdy nie jest nadpisywane automatycznym restartem.
- Kod parowania, certyfikat TLS oraz preferowany port mostu są zachowywane w prywatnych danych aplikacji. Po rekreacji usługi host może zweryfikować ten sam odcisk i sekret zamiast otrzymać nową, niezgodną tożsamość.
- Nieoczekiwane zakończenie natywnego RPC nie wyłącza już całej usługi. Supervisor wykonuje najwyżej trzy próby z opóźnieniami 1, 5 i 15 sekund. Limit obejmuje również procesy, które zdążą otworzyć port i ponownie zaraz zniknąć — nie ma nieskończonej pętli przeładowującej tensory.
- UI i stałe powiadomienie pokazują `ODTWARZANIE SILNIKA`, numer próby oraz stan końcowy. Windows odbiera przez `INFO` liczbę prób, liczbę udanych odtworzeń i błąd końcowy.
- Przed restartem zapisywane są kod i czas pierwotnego wyjścia, log natywny, PSS/RSS/swap, pamięć Androida, historia `LOW_MEMORY` i błędy mostu. Logi są dopisywane między generacjami usługi, a nie kasowane. Po wyczerpaniu limitu listener diagnostyczny i powiadomienie pozostają aktywne, lecz blokady CPU/Wi-Fi są zwalniane. Można skopiować pełny raport lub jawnie ponowić start.
- Odtworzenie procesu nie może wznowić przerwanej komendy protokołu RPC ani trwającego ładowania `llama-server`. Host zachowuje jednak raport przyczyny, a worker pozostaje dostępny do kolejnej próby uruchomienia modelu.

## Wyszukiwanie i szybkie pobieranie publicznych GGUF

- W **Model i API → Wyszukaj na Hugging Face** można znaleźć publiczne repozytoria oznaczone `GGUF`, obejrzeć jednoplikowe warianty oraz rozmiar i kwantyzację. Wieloczęściowe GGUF są świadomie pomijane.
- `Q4_K_M` jest polecanym pierwszym wariantem; opisy wyjaśniają kompromis dla Q3/Q4/Q5/Q6. Licencja nadal należy do autora modelu i użytkownik powinien sprawdzić ją na Hugging Face.
- Wybrany plik jest pobierany ośmioma równoległymi zakresami po 64 MiB. Panel pokazuje bajty, procent, prędkość i ETA. Przycisk zatrzymania zachowuje ukończone części, a **Wznów pobieranie** kontynuuje tylko brakujące zakresy.
- Przed utworzeniem pliku sprawdzane jest wolne miejsce wraz z bezpiecznym zapasem. Każdy zakres wymaga dokładnego `206 Partial Content` i zgodnego `Content-Range`; odpowiedzi o złej długości są odrzucane.
- Gdy repozytorium podaje LFS OID SHA-256, cały plik jest weryfikowany przed publikacją. Następnie YourLM sprawdza strukturę GGUF, atomowo przenosi ukończony plik do prywatnej biblioteki i od razu go wybiera.
- Pobieranie nie uruchamia modelu ani nie wysyła promptów. Inferencja nadal działa lokalnie. Modele dodane wcześniej przez systemowy selektor lub ścieżkę pozostają dostępne bez zmian.
- Modele prywatne lub wymagające zaakceptowania warunków nie są omijane ani pobierane anonimowo; UI pokazuje działający komunikat o ograniczeniu.

## Weryfikacja

CI kompiluje i testuje host Go, waliduje Java/Android, buduje stabilnie podpisane APK ARM64 oraz samodzielny EXE Windows, sprawdza instalację per-user i sumy SHA-256. Testy pobierania używają lokalnego serwera HTTP i obejmują wyszukiwanie, filtrowanie GGUF, równoległe zakresy, wznowienie, anulowanie, niepoprawny `Content-Range` oraz SHA-256.

Nadal prosimy o test na fizycznym Redmi: rozpocząć ładowanie modelu, obserwować `ODTWARZANIE SILNIKA` po rzeczywistym wyjściu procesu oraz skopiować diagnostykę, jeśli trzy próby nie wystarczą. Android może zakończyć cały UID przy skrajnej presji pamięci; żadna foreground service nie daje gwarancji przetrwania takiego zdarzenia.
