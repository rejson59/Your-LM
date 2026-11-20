# YourLM — prywatny klaster GGUF

**0.1.1 · wydanie testowe · Windows x64 i Android ARM64 (9+)**

YourLM uruchamia `llama.cpp` na CPU i potrafi przenieść część warstw modelu na inne urządzenia. To jedna inferencja z podziałem warstw, a nie rozsyłanie całego pytania do kilku niezależnych chatbotów. Nie wymaga konta, chmury, ADB ani roota. Aplikacja nie dodaje własnego filtra treści; zachowanie zależy od modelu i jego szablonu czatu.

| Gotowy plik | Do czego służy |
|---|---|
| [YourLM-Windows-x64-0.1.1.zip](packages/YourLM-Windows-x64-0.1.1.zip) | Pełny, przenośny pakiet Windows. Wypakuj cały katalog; nie wyjmuj samego EXE. |
| [YourLM-Android-arm64.apk](packages/YourLM-Android-arm64.apk) | APK na Redmi lub inny zgodny ARM64. Worker, samodzielny czat lub host klastra. Silnik w zestawie. |
| [SHA256SUMS](packages/SHA256SUMS) | Sumy kontrolne obu plików. |

**Modele nie są dołączone.** Potrzebny jest jednoplikowy, tekstowy GGUF typu *instruct/chat*, ze źródła, któremu ufasz, na licencji odpowiedniej do Twojego zastosowania. Panel, skaner QR i czat działają lokalnie. Pakiet Windows zawiera dwa jawne programy runtime z przypiętego źródła llama.cpp; kontroler nie pobiera ani nie instaluje plików wykonywalnych. Przed uruchomieniem runtime sprawdza jego SHA-256. Po pobraniu pakietu i wag nie jest potrzebny Internet, tylko prywatna sieć między węzłami.

## Pierwsze uruchomienie: Lenovo + Redmi

1. **Najpierw sam laptop.** Pobierz ZIP Windows, przeskanuj go Defenderem i wypakuj **cały** katalog `YourLM-Windows-x64`. Uruchom znajdujący się w nim `YourLM.exe` jako zwykły użytkownik — nigdy jako administrator. Obok musi pozostać katalog `runtime`. Otworzy się lokalny panel w przeglądarce. Zostaw okno konsoli otwarte; gdy przeglądarka nie otworzy się automatycznie, skopiuj cały adres wypisany w konsoli. Nie udostępniaj tego adresu.
2. **Modele i API → Wybierz model.** Wybierz swój lokalny GGUF systemowym oknem pliku. Na pierwszy test proponujemy **3–4B Q4_K_M**, nie 26B. Pozostaw `127.0.0.1`, kliknij **Uruchom model**, poczekaj na „Gotowy” i otwórz **Czat**. Kontroler przed startem sprawdzi dołączony `llama-server.exe`; nie pobiera programu z sieci.
3. **Dołącz Redmi.** Zatrzymaj model na laptopie. Na telefonie zainstaluj APK (instalacja z pliku, nie ze sklepu), połącz urządzenia z tym samym prywatnym Wi-Fi i wybierz **Uruchom worker**. Nie kopiuj wag na worker. Na laptopie wybierz **Klaster → Sparuj worker** i zeskanuj QR kamerą **laptopa**. Alternatywnie przekaż cały prywatny kod sesji zaufanym kanałem i wklej. Dane telefonu powinny pojawić się w inwentarzu.
4. **Uruchom ten sam model ponownie.** Panel pokaże szacowany podział i liczbę warstw wysłanych do workerów. Porównaj tokeny/s po odpowiedzi na ten sam prompt, przy podobnej długości odpowiedzi. Pierwsze ładowanie przez Wi-Fi może trwać długo. Pozostaw worker na ekranie i wstrzymaj pracę, jeśli telefon się przegrzewa. Zatrzymaj model przed odłączaniem urządzeń.

**Inne role.** Na drugim Windows: wypakuj ten sam pełny pakiet, w zakładce **Klaster** rozwiń „Udostępnij ten komputer jako worker”, wybierz prywatny IPv4 i uruchom. Na Androidzie jako host: **Importuj plik GGUF** (systemowy selektor tworzy kopię w prywatnej bibliotece aplikacji) → **Otwórz konsolę hosta** → **Modele i API** → wybierz model z biblioteki. Możesz działać bez workerów albo najpierw sparować inne urządzenia. Host i worker to rozłączne role; jeden worker obsługuje jednego hosta naraz. Host obsługuje do ośmiu sparowanych workerów; większe konfiguracje nie były mierzone.

## Ile naprawdę zmieści się w RAM?

- Na Lenovo 16 GB zacznij od 3–4B, następnie 7–8B Q4. Przy wskazywanych przez Windows ~15,4 GiB pamięci fizycznej limit wag to maksymalnie ~10,3 GiB, mniej przy zajętej pamięci. Na Redmi liczymy fizyczny RAM, **nie** „8 + 8 GB” z rozszerzeniem w pamięci flash.
- Host wykrywa pamięć dostępną dla systemu, zostawia co najmniej 2 GiB z wolnej i ogranicza wagi do ⅔ fizycznej. Budżet workera to mniejsza z wartości: połowa fizycznej pamięci i dostępna pamięć minus 1 GiB. Dane workera są odświeżane przy starcie modelu. Wartości to szacunki, nie rezerwacje systemowe.
- Planista czyta `block_count` i rozmiar GGUF, rezerwuje część na tensory poza blokami i przydziela całe warstwy według budżetu oraz liczby wątków CPU. Ustawia `--rpc`, `-ngl`, `--split-mode layer`, `--tensor-split`. To heurystyka, nie benchmark SoC. Różne warstwy, KV cache, bufory, Wi-Fi i throttling wpływają na wynik. Kontekst tej wersji: 2048 tokenów, jedna równoległa sekwencja.
- **Gemma 4 26B-A4B Abliterated i Mistral Small 3.2 24B Abliterated**: sprawdź *konkretny plik* i obsługę architektury w b12562. Samo 24 mld wag po 4 bity to około 11,2 GiB, **przed** narzutem kwantyzacji, KV i buforów. W MoE mniejsza liczba aktywnych parametrów na token nie oznacza, że można pominąć pamięć pozostałych ekspertów. Nie ma gwarancji załadowania ani użytecznej prędkości. KB10 i Huawei nie są dodawane automatycznie.

## Continue, AnythingLLM i inne klienty OpenAI

Odczytaj **URL `/v1` i klucz** w zakładce **Modele i API** po uruchomieniu serwera. Identyfikator modelu: `yourlm`. Endpointy udostępnia rzeczywisty `llama-server`, m.in. `GET /v1/models` i `POST /v1/chat/completions`. Na tej samej maszynie użyj loopbacku. Na innym komputerze użyj adresu prywatnego hosta wybranego **przed** startem; `127.0.0.1` zawsze oznacza urządzenie klienta.

- **Continue / VS Code:** przycisk „Kopiuj konfigurację Continue” daje konfigurację YAML z odpowiednim adresem i aktualnym kluczem. Wklej wpis `models` do swojej lokalnej konfiguracji Continue (nie nadpisuj innych modeli). Tryb agentowy wymaga obsługi *tool calling* przez konkretny GGUF i szablon rozmowy. Dla takiego modelu dodaj `capabilities: [tool_use]` do jego wpisu i wybierz Agent w Continue. Zacznij od narzędzi tylko do odczytu i zatwierdzaj edycje oraz polecenia. Narzędzia wykonuje Continue; niebezpieczne serwerowe narzędzia plikowe/powłoki `llama-server` są w YourLM jawnie wyłączone. To integracja z istniejącym rozszerzeniem, nie osobna wtyczka. Mały kontekst może ograniczać większe zadania.
- **AnythingLLM:** wybierz kompatybilnego dostawcę OpenAI / Generic OpenAI, podaj URL, klucz i model `yourlm`. Jeśli chcesz w pełni lokalnego RAG, skonfiguruj też lokalny model embeddingów w kliencie. YourLM nie konfiguruje zewnętrznych usług ani telemetrii klientów.

## Prywatność, ograniczenia i diagnostyka

- **RPC jest eksperymentalne.** Surowy RPC nasłuchuje tylko na loopbacku. Między urządzeniami działa tunel TLS: host weryfikuje odcisk certyfikatu z QR, a worker wymaga losowego sekretu sesji. Parowanie nie jest trwałe. Po restarcie workera usuń stary wpis i użyj nowego kodu. Chroń QR i schowek. To nie izoluje przed złośliwym oprogramowaniem już działającym na urządzeniu.
- **API LAN to HTTP z kluczem Bearer, nie HTTPS.** Korzystaj wyłącznie w zaufanej prywatnej sieci; nie przekierowuj portów na routerze. Do niezaufanej sieci potrzebny jest osobny bezpieczny transport. Panel zarządzania nie jest wystawiany w LAN. Klucz API zmienia się po każdym uruchomieniu modelu. Historia w panelu pozostaje w pamięci karty, a silnik zapisuje lokalny log diagnostyczny; zewnętrzne klienty mogą przechowywać własną historię.
- Jeśli parowanie nie działa: sprawdź wspólne Wi-Fi, brak izolacji klientów/Guest Wi-Fi, prywatny IPv4, aktualny kod, zgodę na kamerę lub ręczne wklejenie kodu. Zapora Windows powinna dopuszczać połączenia tylko w profilu prywatnym. Android szuka interfejsu Wi-Fi `wlan*`.
- Po utracie workera zatrzymaj serwer, usuń niedostępny węzeł i uruchom model ponownie. Brak migracji trwającej generacji i automatycznego failover. Na HyperOS pozostaw aplikację na pierwszym planie. Zapisz model na hoście z odpowiednim zapasem miejsca. Podczas ładowania bardzo dużych plików system może przerwać proces mimo pozytywnej estymacji.
- Dane Windows: `%AppData%\YourLM\` (`models`, `engine.log`). Kontroler i katalog `runtime` pozostają tam, gdzie wypakowano pakiet. Kontroler nie pobiera programów i odrzuca zmieniony runtime. Nie zastępuj plików runtime inną wersją protokołu RPC.
- EXE nie ma komercyjnego podpisu Authenticode. Ma jawne informacje wersji, manifest `asInvoker` i nie wymaga administratora. APK podpisano kluczem **wydania testowego**, nie produkcyjnym kluczem sklepowym. Weryfikuj sumy plików. Jeśli antywirus zgłosi alarm, nie zezwalaj ani nie dodawaj wyjątku — zgłoś pełną nazwę detekcji i SHA-256. Własny build podpisany innym kluczem wymaga odinstalowania poprzedniego APK (to usuwa prywatne kopie modeli). Wagi źródłowe zachowaj osobno.
- W tej wersji nie ma generowania obrazów, Vision, automatycznego pobierania modeli ani obsługi wieloczęściowych GGUF. Kod i pliki są gotowe do testów; nie testowano instalacji na fizycznym Lenovo ani Redmi.

## Testy i budowanie

Kod hosta: Go (bez zewnętrznych modułów), interfejs statycznie osadzony, bez CDN. Android: natywna Activity w Javie, dwie natywne binarki CPU/RPC i ten sam panel hosta w WebView. Licencje komponentów są w `licenses/`, `host/web/` i źródłach vendora.

```sh
go test ./...
git clone https://github.com/ggml-org/llama.cpp /tmp/yourlm-llama
git -C /tmp/yourlm-llama checkout c0b97be0b297177a88df08e96c01420976bb18d0
LLAMA_SRC=/tmp/yourlm-llama scripts/build-windows.sh
```

Windows wymaga Go 1.25.5, Zig 0.14.1, CMake i Ninja. Skrypt buduje dwa statyczne programy runtime ze źródła, wpisuje ich SHA-256 do kontrolera i tworzy zwykły katalog — bez instalatora i packera. Kontroler Go zachowuje symbole (nie używa `-s -w`); runtime usuwa pomocnicze symbole linkera, dzięki czemu dwa czyste buildy są identyczne bajt w bajt. Manifest oraz metadane PE są generowane z `windows/`. Szczegółowy opis zachowania i granic zaufania znajduje się w [SECURITY.md](SECURITY.md).

APK (Linux x64): Go 1.25.5, Zig 0.14.1 na `PATH`, CMake, Make, JDK 17, Android SDK platform/build-tools 34, `zip`. Skrypty niczego nie pobierają i nie zawierają klucza podpisującego.

```sh
git clone --depth 1 --branch b12562 https://github.com/ggml-org/llama.cpp /tmp/yourlm-llama
LLAMA_SRC=/tmp/yourlm-llama scripts/build-native.sh
# Ustaw ANDROID_HOME, JAVA_HOME i PATH. Przygotuj własny keystore (alias yourlm).
# KEYSTORE = ścieżka poza repo, KS_PASS = hasło podane bezpiecznie w środowisku.
RPC_BIN="$PWD/build/android-native/bin/ggml-rpc-server" \
SERVER_BIN="$PWD/build/android-native/bin/llama-server" scripts/package-android.sh
```

Testy jednostkowe obejmują metadane GGUF, budżety, walidację kodów, granice panelu oraz rzeczywiste połączenie TLS (odrzucenie złego sekretu i certyfikatu). Opcjonalny test integracyjny:

```sh
python3 tests/tiny_model.py /tmp/yourlm-test.gguf
# Uruchom przypięty ggml-rpc-server na 127.0.0.1:50052, tylko na czas testu.
TEST_ENGINE=/sciezka/do/llama-server TEST_MODEL=/tmp/yourlm-test.gguf \
TEST_RPC=127.0.0.1:50052 go test -v -run TestRealCluster ./host
```

W środowisku budowania wykonano inferencję syntetycznego GGUF z dwoma procesami ARM64 pod QEMU, przez rzeczywisty tunel TLS, z 2 z 4 warstw na workerze. Uzyskano odpowiedź `/v1/chat/completions`; osobno sprawdzono odrzucenie zapytania bez klucza (401). **To test ścieżki wykonania, nie jakości modelu ani szybkości fizycznego klastra.**
