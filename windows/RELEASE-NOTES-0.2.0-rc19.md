# YourLM 0.2.0-rc19 — rzeczywisty wolny RAM workera i krótki raport techniczny

RC19 naprawia przyczynę awarii widocznej podczas ładowania modelu Balanced na Redmi 8 GB. To nie ostrzeżenia o `mmap` ani nieużywane tensory `blk.40` kończyły proces. Standardowy backend CPU llama.cpp na systemach innych niż Windows zgłaszał do `--fit`, że **cały fizyczny RAM jest wolny**. Na Androidzie pomijało to pamięć zajętą przez system i aplikacje. W rezultacie planista mógł przydzielić workerowi zdecydowanie za dużo warstw mimo rezerwy 2816 MiB; raport z fizycznego telefonu pokazał 3 417 985 024 B (3,18 GiB) swap procesu RPC, szczyt RSS około 1,56 GiB i tylko około 0,56 GiB dostępnego RAM, po czym połączenie RPC zostało przerwane.

## Poprawka pamięci

- Natywny worker `b12562-ylm-rpc6` odczytuje na Linuxie i Androidzie `MemAvailable` z `/proc/meminfo` i właśnie tę wartość przekazuje planiście `--fit`.
- Zużycie RAM przez Androida i inne aplikacje jest teraz odejmowane **przed** wyborem warstw dla workera. Dotychczasowa rezerwa zależna od największego tensora nadal obowiązuje.
- Bezpieczny wynik może przypisać Redmi mniej warstw niż RC17/RC18. Pierwsze ładowanie powinno dzięki temu przesłać mniej danych i nie doprowadzać telefonu do wielogigabajtowego swapu.
- Niezależny bezpiecznik Windows zatrzymuje ładowanie, jeśli Android ma mniej niż 1 GiB dostępnej pamięci w trzech kolejnych pomiarach. Zamiast czekać na zabicie procesu przez system zapisuje jednoznaczny wpis `RPC_MEMORY_GUARD`.
- Test RPC uruchamiany w CI sprawdza teraz, że zdalny backend zwraca wartość `MemAvailable` mniejszą od całego fizycznego RAM, a następnie nadal przechodzi miss, hit i naprawę uszkodzonego cache.

## Diagnostyka bez wklejania megabajtów

- **Kopiuj krótki raport** zachowuje wersję i commit, przyczynę zakończenia, transport, pomiary Wi‑Fi/USB, RAM, RSS/PSS/swap, stan cache i mostu, ostatnie zdarzenia Androida oraz istotną końcówkę logu natywnego.
- Powtarzalne wpisy transferu tensorów i stare wielowierszowe stosy `Socket closed` są pomijane.
- **Kopiuj rozszerzony log** pozostaje dostępny obok, gdy będzie potrzebny surowy materiał.
- Pobieranie raportu po awarii ponawia całą odpowiedź drugim uwierzytelnionym łączem, jeżeli preferowany tunel zaakceptował TLS, ale zakończył odpowiedź `EOF`.

## Bez wyścigu przy aktualizacji

- Helper aktualizacji czeka teraz na rzeczywiste zakończenie poprzedniego procesu Windows i zwolnienie blokady magazynu, zanim podmieni EXE i uruchomi RC19.
- Usuwa to fałszywy komunikat „Inne okno YourLM nadal zarządza tą biblioteką” pojawiający się po zwykłym restarcie z panelu aktualizacji. Rzeczywiste równoległe uruchomienie drugiej kopii nadal jest bezpiecznie blokowane.

## Aktualizacja

1. Zatrzymaj model i worker RC17/RC18.
2. Uruchom nowe `YourLM.exe` jako zwykły użytkownik.
3. W **Telefon USB** wybierz **Zainstaluj lub zaktualizuj YourLM**, aby Redmi otrzymał RC19 (`versionCode 22`). EXE wymaga workera `b12562-ylm-rpc6`, więc stara aplikacja zostanie celowo odrzucona.
4. Uruchom worker ponownie. `adb install -r` zachowuje prywatne dane, klucz parowania i cache; nie usuwaj cache ani modelu.
5. Uruchom ten sam model Balanced. Jeżeli próba nadal się zatrzyma, wybierz **Kopiuj krótki raport** — nie trzeba już przesyłać całego `engine.log`.

RC19 zachowuje zweryfikowane scalanie magazynu C:/D: i wszystkie zabezpieczenia publikacji wprowadzone w RC18.
