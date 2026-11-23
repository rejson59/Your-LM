# YourLM 0.2.0-rc17 — bezpieczne wznowienie migracji i odświeżony magazyn

RC17 naprawia rzeczywisty błąd pierwszej migracji Lenovo z C: na D:, w którym pozostawiony duży plik `*.migrating-part` kończył start komunikatem `The file exists`. W opisywanym przypadku RC16 postąpiło bezpiecznie — nie opublikowało niepełnego D: i zachowało źródło `%AppData%\YourLM` — ale nie potrafiło wykorzystać prawidłowego prefiksu. RC17 potrafi go sprawdzić i wznowić.

## Restartowalna migracja C: → D:

- Każdy istniejący `D:\YourLM\Data.migrating\…\*.migrating-part` jest najpierw sprawdzany jako zwykły, niesymlinkowany plik.
- Jeśli rozmiar nie przekracza źródła, RC17 porównuje **cały istniejący prefiks bajt po bajcie**. Zgodną część dopisuje od pierwszego brakującego bajtu; pełną zgodną część zatwierdza bez ponownego kopiowania.
- Część o złym prefiksie lub nadmiarowym rozmiarze jest odrzucana pojedynczo i tworzona ponownie. Program nie kasuje w ciemno całego dużego `Data.migrating`.
- Błąd odczytu, zapisania, `fsync`, blokada antywirusa/innego procesu albo brak miejsca zatrzymuje publikację. Prawidłowy prefiks pozostaje do następnego wznowienia, a źródło C: nie jest usuwane.
- Ukończony plik stagingu, który nie odpowiada już źródłu, jest zastępowany pojedynczo. Zwykły osierocony wpis nieobecny w źródle jest usuwany wyłącznie ze stagingu; dowiązanie, specjalny typ albo wpis, którego nie można bezpiecznie odczytać/usunąć, zatrzymuje operację.
- Gotowe pliki są porównywane SHA-256, a następnie RC17 ponownie sprawdza dokładne drzewo źródło → staging. Brakujący, nieprawidłowy lub nadal tymczasowy wpis uniemożliwia publikację.
- Znacznik `.yourlm-storage-v1` jest zapisywany i synchronizowany dopiero po pełnej kontroli. Jedyną publikacją jest atomowa zmiana nazwy `Data.migrating` na `Data`.
- Oryginał na C: jest atomowo odkładany i usuwany dopiero po publikacji D:. Jeśli Windows nie pozwoli go odłożyć, D: pozostaje aktywne, a interfejs wyraźnie pokazuje zachowaną nieaktywną kopię.

## Ochrona przed drugim oknem i awarią

Migrację chroni wyłączna blokada pliku systemu operacyjnego w `D:\YourLM`. Dwie instancje nie mogą jednocześnie dopisywać stagingu ani publikować D:. Druga instancja kończy start z instrukcją, zamiast wracać do zapisywalnego C: podczas snapshotu pierwszej. Blokada jest własnością otwartego uchwytu, więc po awarii procesu system zwalnia ją automatycznie; pozostawione metadane są rozpoznawane jako odzyskanie po przerwaniu, a nie permanentna blokada.

## Czytelny stan zamiast jednego długiego błędu

Zakładka **Testy → Magazyn YourLM** ma odświeżoną hierarchię i responsywny panel:

- duży status aktywnego dysku i odrębne stany `ODZYSKANO`, `D: AKTYWNE`, `C: BEZPIECZNE` lub `D: działa; kopia C: zachowana`;
- czytelny przepływ źródło → synchronizacja/SHA-256 → cel;
- liczniki wznowionych bajtów, ponownie użytych plików, odrzuconych części i końcowej weryfikacji;
- osobna, krótka instrukcja działania oraz rozwijane szczegóły techniczne;
- jawne oznaczenie `Data.migrating` jako nieaktywnego stagingu i D: jako celu po naprawie;
- paski wolnego miejsca, poprawiony układ mobilny, ciemny motyw i odświeżony pasek nawigacji działające także w natywnym WebView2.

Raport tekstowy zachowuje ten sam podział: status, odzyskanie, weryfikację, zachowane źródło, działanie i dopiero na końcu techniczny błąd.

## Jeden Redmi i ten sam złoty środek

RC17 scala rekord ADB `24090RA29G` z rekordem workera `Xiaomi 24090RA29G`. Raport pokazuje jeden fizyczny Redmi z bogatszym zestawem: MediaTek MT6878, 8 rdzeni / 2,50 GHz, ARM dot-product, fizyczny RAM, UFS, bateria i oba źródła diagnostyki — bez pustego duplikatu z wartościami 0.

Autopilot nadal wybiera jeden audytowany praktyczny model: **Qwen3.6-35B-A3B Abliterated · Balanced Q2/Q4**. Nie nazywa aligned GGUF uncensored i nie sugeruje, że abliteration zwiększa inteligencję. Dopóki magazyn pozostaje w bezpiecznym fallbacku C:, przycisk automatycznego zastosowania jest wstrzymany; po zweryfikowanym D: można zbudować profil i wykonać rzeczywisty test tokenów/s. **Qwen3-Coder-Next 80B-A3B Abliterated IQ3_XS** pozostaje warunkowym kandydatem wymagającym pomiaru 35B i osobno audytowanej receptury `qwen3next`.

## Aktualizacja na Lenovo + Redmi

1. **Nie usuwaj ręcznie** `%AppData%\YourLM` ani `D:\YourLM\Data.migrating` przed pierwszym udanym startem RC17.
2. Zamknij poprzednie okna YourLM i uruchom nowe `YourLM.exe` jako zwykły użytkownik. Przy dużym GGUF kontrola prefiksu i SHA-256 może potrwać; aplikacja nie pokazuje okna, dopóki przygotowanie bezpiecznego magazynu nie zostanie zakończone.
3. Po otwarciu przejdź do **Testy → Magazyn YourLM**. Oczekiwany wynik to `ODZYSKANO` lub `D: AKTYWNE`, aktywne dane `D:\YourLM\Data` i D: oznaczone jako aktywny dysk.
4. Jeśli widzisz `C: BEZPIECZNE`, pozostaw źródło, wykonaj podaną krótką instrukcję i rozwiń szczegóły tylko do zgłoszenia. Nie uznawaj `Data.migrating` za gotową bibliotekę.
5. W **Telefon USB** wybierz ponownie **Zainstaluj lub zaktualizuj YourLM**, aby Redmi otrzymał RC17 (`versionCode 20`). `adb install -r` zachowuje prywatne dane aplikacji.
6. Uruchom pełny test ponownie. Po migracji raport powinien pokazać około 321 GiB wolnego na aktywnym D:, jeden scalony Redmi oraz warunkową ocenę 80B opartą na D:, a nie wcześniejszych 31 GiB wolnych na C:.

Modele, ustawienia, cache i raporty pozostają poza EXE i są zachowywane przy aktualizacji oraz odinstalowaniu.
