# YourLM 0.2.0-rc16 — test Lenovo + Redmi, autopilot i magazyn D:

RC16 jest wydaniem diagnostycznym przygotowanym dla konfiguracji z małym dyskiem systemowym C: oraz dużym dyskiem D:. Nie zmienia jeszcze audytowanej receptury modelu bez pomiaru fizycznych urządzeń.

## Automatyczne przeniesienie z C: na D:

- Gdy `D:` jest zapisywalnym dyskiem stałym z co najmniej 128 GiB pojemności i 64 GiB wolnego miejsca, program instaluje się w `D:\YourLM\App\YourLM.exe`.
- Modele, części pobierania, runtime, ADB, APK, cache pagera/RPC, logi, raporty i przyszłe aktualizacje są przechowywane w `D:\YourLM\Data`.
- Dotychczasowy `%AppData%\YourLM` jest kopiowany wznawialnie. Każdy plik jest zapisywany do pliku tymczasowego, synchronizowany i porównywany przez SHA-256; źródło jest usuwane dopiero po pełnym powodzeniu.
- Nowa kopia EXE jest uruchamiana przed wycofaniem starego katalogu `%LocalAppData%\Programs\YourLM`. Skrót Menu Start i wpis Zainstalowanych aplikacji są przepinane na D:.
- Awaria, konflikt plików, brak miejsca lub brak dostępu nie niszczy starej biblioteki. YourLM wraca wtedy do danych na C: i pokazuje ostrzeżenie.
- Odinstalowanie nadal zachowuje prywatną bibliotekę modeli i raportów.

## Nowa zakładka Testy

Pełny test działa lokalnie i ma sześć etapów:

1. identyfikacja Lenovo, Windows, CPU, rdzeni, fizycznego RAM-u i GPU przez Windows CIM;
2. krótka wielowątkowa próbka CPU;
3. bezpieczna próbka zapisu z `fsync` i ciepłego odczytu wyłącznie we własnym katalogu YourLM na D:;
4. odczyt Redmi przez autoryzowane USB/ADB: dokładny model, Android, SoC, ABI, rdzenie/częstotliwości, ARM dot-product, fizyczny RAM, wolne UFS, bateria i temperatura;
5. tymczasowa próbka 128 MiB pamięci telefonu oraz rzeczywisty uwierzytelniony benchmark Wi-Fi i tunelu USB w obu kierunkach;
6. jedna rekomendacja autopilota i lokalny raport tekstowy.

Tymczasowe próbki są usuwane po teście. Raport nie zawiera numeru seryjnego ADB, nazwy hosta, kodu parowania ani klucza API. Jest zapisywany w `D:\YourLM\Data\diagnostics\latest-report.txt` i można go skopiować jednym przyciskiem.

Po uruchomieniu modelu przycisk **Testuj działający model** wykonuje krótką odpowiedź bez historii czatu, zapisuje prompt processing, tokeny/s i sprawdza, czy model wykonał ścisłą instrukcję zwrócenia jednego tekstu.

## Granice autopilota i wybór modelu

- Autopilot wybiera obecny audytowany złoty środek: **Qwen3.6-35B-A3B Abliterated Balanced Q2/Q4**.
- **Qwen3-Coder-Next 80B-A3B Abliterated IQ3_XS** pozostaje kandydatem maksimum. Program ocenia progi pamięci, miejsca, dot-product i prędkości 35B, ale nie pobiera 80B w ciemno bez nowej receptury `qwen3next`, kontroli jakości kwantyzacji i pomiaru na fizycznym Redmi.
- Autopilot może uruchamiać wyłącznie własne testy, przenosić własny katalog i zastosować przypiętą recepturę modelu po widocznym potwierdzeniu. Nie otrzymuje dowolnych narzędzi powłoki, nie przegląda dokumentów i nie omija systemowych potwierdzeń Androida.

## Aktualizacja

Uruchom nowe `YourLM.exe` jako zwykły użytkownik. Pierwsza migracja może potrwać, jeśli na C: istnieją duże modele lub części pobierania; nie przerywaj jej. Po otwarciu aplikacji sprawdź w **Testy → Magazyn YourLM**, czy program i dane wskazują D:. Następnie ponownie użyj **Telefon USB → Zainstaluj lub zaktualizuj YourLM**, aby Redmi otrzymał APK RC16 (`versionCode 19`).
