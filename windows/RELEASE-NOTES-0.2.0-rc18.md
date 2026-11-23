# YourLM 0.2.0-rc18 — bezpieczne scalenie zachowanego C: z opublikowanym D:

RC18 naprawia rzeczywisty stan odzyskiwania zgłoszony po RC17. `D:\YourLM\Data` miało prawidłowy znacznik wcześniejszej publikacji, lecz aktywne `%AppData%\YourLM` zawierało dodatkowy katalog `components\0.2.0-rc13-e9711eb1537f4f64`. RC17 prawidłowo nie usunęło jedynej znanej kopii tego komponentu i wróciło do C:, ale przy każdym starcie zatrzymywało się na komunikacie `brak wpisu ... w przeniesionej bibliotece`. RC18 potrafi bezpiecznie połączyć oba drzewa zamiast pozostawać w trwałym fallbacku.

## Reguła scalania bez zgadywania

- Wynik jest pełną sumą wpisów z aktywnego C: oraz opublikowanego D:.
- Przy tej samej ścieżce **wersja z aktywnego C: ma pierwszeństwo**. Dotyczy to również konfliktu plik/katalog.
- Pliki i katalogi istniejące wyłącznie na D: pozostają zachowane. RC18 nie cofa modeli ani pobrań, które pojawiły się tylko w opublikowanej bibliotece.
- Znaczniki sterujące nie są kopiowane jako dane. Nowy znacznik powstaje dopiero po końcowej kontroli wyniku.
- Dowiązanie, urządzenie, potok lub inny specjalny wpis zatrzymuje operację. Osierocone poddrzewo stagingu jest najpierw w całości sprawdzane, a dopiero potem usuwane od liści; program nie przekazuje nieodwiedzonego drzewa do rekursywnego kasowania.

## Nieaktywny staging, SHA-256 i publikacja transakcyjna

1. RC18 buduje sumę wyłącznie w nieaktywnym `D:\YourLM\Data.migrating`. Aktywne C: nie jest nadpisywane ani usuwane podczas budowy.
2. Prawidłowe gotowe pliki i prefiksy `*.migrating-part` mogą zostać sprawdzone i użyte ponownie. Pliki istniejące tylko na D: są zachowywane bez zmiany treści; gdy twarde łącze NTFS nie jest dostępne, program przechodzi na zwykłą wznawialną kopię.
3. RC18 wylicza oczekiwaną sumę C:+D:, porównuje rozmiar i SHA-256 **każdego pliku**, odrzuca brakujące, dodatkowe, kolidujące nazwą lub nieprawidłowe typy i zapisuje manifestowy skrót całego drzewa.
4. Dopiero zweryfikowany staging otrzymuje zsynchronizowany znacznik `reconciled`.
5. Publikacja odkłada stare D: pod `Data.reconcile-backup`, atomowo zmienia nazwę stagingu na `Data`, ponownie sprawdza opublikowane drzewo względem znacznika i dopiero wtedy sprząta kopię transakcyjną.
6. Zachowane C: jest wycofywane dopiero po pełnej, zweryfikowanej publikacji. Jeśli Windows blokuje sprzątanie, aktywne dane pozostają na D:, a raport pokazuje pozostawioną kopię i zalecane działanie.

## Odzyskiwanie po przerwaniu

- Jeśli proces zakończy się po odłożeniu starego D:, ale przed publikacją nowego drzewa, następny start przywraca poprzednie opublikowane `Data`.
- Jeśli przerwanie nastąpi po publikacji nowego drzewa, RC18 ponownie liczy liczbę plików, bajty i manifest SHA-256. Stare D: może zostać usunięte dopiero po zgodności ze znacznikiem scalania.
- Uszkodzona lub niepełna nowa publikacja nie może skasować kopii rollback ani źródła C:.
- Problem dotyczący wyłącznie sprzątnięcia starego `Data.reconcile-backup` nie przełącza aplikacji na pusty fallback C:. Zweryfikowane D: pozostaje aktywne, kopia zostaje zachowana i jest jawnie zgłoszona do kolejnej próby.
- Jeśli Windows nie pozwoli wycofać C: już po pełnej publikacji, RC18 zapisuje w znaczniku, że jest to **nieaktywna kopia do sprzątnięcia**. Kolejny start nie nałoży jej ponownie na bibliotekę D:, która mogła już otrzymać nowe modele, logi lub ustawienia.

## Widoczny wynik

Panel **Testy → Magazyn YourLM** i raport tekstowy rozpoznają stan `SCALONO`. Pokazują aktywne `D:\YourLM\Data`, pełną liczbę zweryfikowanych plików i bajtów, użyte ponownie dane, odzyskane prefiksy oraz ewentualną zachowaną kopię. Autopilot pozostaje zablokowany podczas fallbacku i odblokowuje się dopiero po zweryfikowanej publikacji D:.

Regresje automatyczne obejmują dokładny brak komponentu z raportu, zachowanie pliku istniejącego tylko na D:, pierwszeństwo kolidującego pliku C:, wznowienie prefiksu, przerwanie przed i po atomowej podmianie, uszkodzenie nowej publikacji, ponowną próbę po zachowanej kopii oraz niebezpieczne poddrzewo stagingu. Bramka Windows odtwarza także rzeczywisty scenariusz dwóch kolejnych uruchomień na zarządzanym magazynie.

## Aktualizacja Lenovo + Redmi

1. **Nie usuwaj ręcznie** `%AppData%\YourLM`, `D:\YourLM\Data`, `D:\YourLM\Data.migrating` ani `D:\YourLM\Data.reconcile-backup`.
2. Zamknij wszystkie poprzednie okna YourLM i uruchom `YourLM.exe` RC18 jako zwykły użytkownik. Kontrola dużych modeli przez SHA-256 może potrwać; nie przerywaj jej.
3. Oczekiwany wynik w **Testy → Magazyn YourLM** to `SCALONO` lub `D: AKTYWNE`, a aktywna ścieżka danych to `D:\YourLM\Data`.
4. Jeśli nadal widzisz `C: BEZPIECZNE`, niczego nie kasuj. Skopiuj raport ze szczegółami technicznymi; źródło C: i nieaktywny staging zostały zachowane.
5. W **Telefon USB** ponownie wybierz **Zainstaluj lub zaktualizuj YourLM**, aby Redmi otrzymał RC18 (`versionCode 21`). `adb install -r` zachowuje prywatne dane aplikacji.

Modele, ustawienia, cache, komponenty i raporty pozostają poza EXE i są zachowywane przy aktualizacji oraz odinstalowaniu.
