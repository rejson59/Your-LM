# YourLM — prywatny klaster GGUF

**0.2.0-rc1 · nowa baza testowa · Windows x64 i Android ARM64 (9+)**

YourLM uruchamia `llama.cpp` na CPU i potrafi przenieść część warstw modelu na inne urządzenia. To jedna inferencja z podziałem warstw, a nie rozsyłanie całego pytania do kilku niezależnych chatbotów. Nie wymaga konta, chmury ani roota. Aplikacja nie dodaje własnego filtra treści; zachowanie zależy od modelu i jego szablonu czatu.

Gotowe pliki są publikowane na stronie [GitHub Releases](https://github.com/rejson59/Your-LM/releases):

| Gotowy plik | Do czego służy |
|---|---|
| `YourLM.exe` | Samodzielna aplikacja Windows. Silnik, ADB i APK są w środku — bez ZIP-a i plików obok. |
| `YourLM-Android-arm64.apk` | APK na Redmi lub inny zgodny ARM64. Worker, samodzielny czat lub host klastra. |
| `SHA256SUMS` | Sumy kontrolne plików wydania. |

**Modele nie są dołączone.** Potrzebny jest jednoplikowy, tekstowy GGUF typu *instruct/chat*, ze źródła, któremu ufasz, na licencji odpowiedniej do Twojego zastosowania. Panel, skaner QR i czat działają lokalnie. Pierwszy start EXE rozpakowuje przypięty runtime i minimalny ADB do prywatnego `%AppData%\YourLM\`; pliki są sprawdzane i automatycznie odtwarzane przy uszkodzeniu. Internet jest potrzebny tylko do sprawdzania/pobierania aktualizacji, nie do inferencji.

## Pierwsze uruchomienie: Lenovo + Redmi

1. **Najpierw sam laptop.** Pobierz `YourLM.exe`, sprawdź sumę/Defendera i uruchom jako zwykły użytkownik — nigdy jako administrator. Niczego nie wypakowuj. Otworzy się lokalny panel w przeglądarce; nie udostępniaj jego prywatnego adresu.
2. **Modele i API → Wybierz model.** Wybierz swój lokalny GGUF systemowym oknem pliku. Na pierwszy test proponujemy **3–4B Q4_K_M**, nie 26B. Pozostaw `127.0.0.1`, kliknij **Uruchom model**, poczekaj na „Gotowy” i otwórz **Czat**. Kontroler przed startem sprawdzi dołączony `llama-server.exe`; nie pobiera programu z sieci.
3. **Zainstaluj aplikację na Redmi.** W panelu Windows otwórz **Telefon USB** i wykonaj trzy pokazane kroki. W HyperOS włącz Debugowanie USB oraz, jeśli jest wymagane, **Instalowanie przez USB**. YourLM wykryje urządzenie i użyje `adb install -r`, aby instalować kolejne wersje bez kasowania danych. APK z Releases pozostaje alternatywą; Android zawsze pokazuje systemowe potwierdzenie instalacji.
4. **Dołącz Redmi jako worker.** Zatrzymaj model na laptopie, połącz urządzenia z tym samym prywatnym Wi-Fi i na telefonie wybierz **Pomóż laptopowi → Uruchom worker**. Nie kopiuj wag na worker. Telefon pokaże **adres parowania** i 20-znakowy **kod parowania**. Na laptopie wybierz **Urządzenia → Dodaj telefon** i przepisz oba pola. Kamera laptopa jest tylko opcją; telefon niczego nie skanuje.
5. **Uruchom ten sam model ponownie.** Panel pokaże szacowany podział i liczbę warstw wysłanych do workerów. Porównaj tokeny/s po odpowiedzi na ten sam prompt, przy podobnej długości odpowiedzi. Pierwsze ładowanie przez Wi-Fi może trwać długo. Pozostaw worker na ekranie i wstrzymaj pracę, jeśli telefon się przegrzewa. Zatrzymaj model przed odłączaniem urządzeń.

**Inne role.** Na drugim Windows uruchom ten sam samodzielny EXE, w zakładce **Urządzenia** rozwiń „Udostępnij ten komputer innemu hostowi”, wybierz prywatny IPv4 i uruchom. Na Androidzie jako host: **Model na telefonie → Dodaj plik GGUF** (systemowy selektor tworzy kopię w prywatnej bibliotece aplikacji) → **Otwórz konsolę** → **Model i API** → wybierz model z biblioteki. Możesz działać bez workerów albo najpierw sparować inne urządzenia. Host i worker to rozłączne role; jeden worker obsługuje jednego hosta naraz. Host obsługuje do ośmiu sparowanych workerów; większe konfiguracje nie były mierzone.

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

- **RPC jest eksperymentalne.** Surowy RPC nasłuchuje tylko na loopbacku. Między urządzeniami działa tunel TLS z przypiętym odciskiem certyfikatu i sekretem sesji. Przy ręcznym parowaniu 20-znakowy kod ma 100 bitów losowości i nie jest wysyłany przez sieć: host przesyła losowe wyzwanie, worker odpowiada HMAC obejmującym wyzwanie, adres TLS i odcisk certyfikatu, a dopiero potem host otwiera przypięte TLS. Pełny QR przenosi równoważne dane bezpośrednio. Parowanie nie jest trwałe. Po restarcie workera usuń stary wpis i użyj nowych danych. Nie publikuj kodu ani QR. To nie izoluje przed złośliwym oprogramowaniem już działającym na urządzeniu.
- **API LAN to HTTP z kluczem Bearer, nie HTTPS.** Korzystaj wyłącznie w zaufanej prywatnej sieci; nie przekierowuj portów na routerze. Do niezaufanej sieci potrzebny jest osobny bezpieczny transport. Panel zarządzania nie jest wystawiany w LAN. Klucz API zmienia się po każdym uruchomieniu modelu. Historia w panelu pozostaje w pamięci karty, a silnik zapisuje lokalny log diagnostyczny; zewnętrzne klienty mogą przechowywać własną historię.
- Jeśli parowanie nie działa: sprawdź wspólne Wi-Fi, brak izolacji klientów/Guest Wi-Fi, prywatny IPv4 oraz aktualny adres i kod pokazany na telefonie. Przepisz wszystkie cztery grupy kodu; nie wpisuj pełnego `YLM1|…` w pole krótkiego kodu. Kamera laptopa jest tylko opcją. Zapora Windows powinna dopuszczać połączenia wyłącznie w profilu prywatnym. Android szuka interfejsu Wi-Fi `wlan*`.
- Po utracie workera zatrzymaj serwer, usuń niedostępny węzeł i uruchom model ponownie. Brak migracji trwającej generacji i automatycznego failover. Na HyperOS pozostaw aplikację na pierwszym planie. Zapisz model na hoście z odpowiednim zapasem miejsca. Podczas ładowania bardzo dużych plików system może przerwać proces mimo pozytywnej estymacji.
- Dane Windows: `%AppData%\YourLM\` (`models`, ustawienia, log i wersjonowane `components`). Aktualizacja podmienia tylko EXE; modele i konfiguracja pozostają. Runtime, ADB oraz instalacyjny APK są odzyskiwane z pliku wydania, a zmienione komponenty są odtwarzane przed użyciem.
- EXE nie ma komercyjnego podpisu Authenticode. Ma jawne informacje wersji, manifest `asInvoker` i nie wymaga administratora. Od 0.2.0-rc1 APK używa stałej tożsamości aktualizacji projektu (certyfikat SHA-256 `bb2639f3d49a2d7db0845884e76d6bb51152a6e9c0df92fcfe885a26c5de94a6`), przeznaczonej do wszystkich następnych wydań. Weryfikuj sumy plików. Jeśli antywirus zgłosi alarm, nie zezwalaj ani nie dodawaj wyjątku — zgłoś pełną nazwę detekcji i SHA-256. Własny build podpisany innym kluczem wymaga odinstalowania poprzedniego APK (to usuwa prywatne kopie modeli). Wagi źródłowe zachowaj osobno.
- W tej wersji nie ma generowania obrazów, Vision, automatycznego pobierania modeli ani obsługi wieloczęściowych GGUF. Kod i pliki są gotowe do testów; nie testowano instalacji na fizycznym Lenovo ani Redmi.

## Testy i budowanie

Kod hosta: Go (bez zewnętrznych modułów), interfejs statycznie osadzony, bez CDN. Android: natywna Activity w Javie, dwie natywne binarki CPU/RPC i ten sam panel hosta w WebView. Licencje komponentów są w `licenses/`, `host/web/` i źródłach vendora.

```sh
go test ./...
git clone https://github.com/ggml-org/llama.cpp /tmp/yourlm-llama
git -C /tmp/yourlm-llama checkout c0b97be0b297177a88df08e96c01420976bb18d0
LLAMA_SRC=/tmp/yourlm-llama scripts/build-windows.sh
```

Windows wymaga Go 1.25.5, Zig 0.14.1, CMake i Ninja oraz gotowego `packages/YourLM-Android-arm64.apk`. Skrypt buduje dwa statyczne programy runtime ze źródła i osadza je wraz z przypiętym minimalnym ADB oraz APK w jednym `packages/YourLM.exe`. Kontroler weryfikuje runtime przed uruchomieniem. Manifest oraz metadane PE są generowane z `windows/`; po zmianie wersji uruchom `scripts/regenerate-windows-resource.sh`. Szczegółowy opis zachowania i granic zaufania znajduje się w [SECURITY.md](SECURITY.md).

APK (Linux x64): Go 1.25.5, CMake, Make, JDK 17, Android SDK platform/build-tools 34, `zip` oraz **oficjalny Android NDK 27.2.12479018**. Androidowych silników nie wolno budować jako ogólne `aarch64-linux-musl`: skrypt wymaga sysrootu NDK, ABI `arm64-v8a`, API 28 i statycznego C++ runtime. Skrypty niczego nie pobierają i nie zawierają klucza podpisującego.

```sh
git clone --depth 1 --branch b12562 https://github.com/ggml-org/llama.cpp /tmp/yourlm-llama
export LLAMA_SRC=/tmp/yourlm-llama
export ANDROID_NDK_HOME="$ANDROID_HOME/ndk/27.2.12479018"
scripts/build-native.sh
# Ustaw ANDROID_HOME, JAVA_HOME i PATH. Przygotuj własny keystore (alias yourlm).
# KEYSTORE = ścieżka poza repo, KS_PASS = hasło podane bezpiecznie w środowisku.
RPC_BIN="$PWD/build/android-native/bin/ggml-rpc-server" \
SERVER_BIN="$PWD/build/android-native/bin/llama-server" scripts/package-android.sh
```

`verify-android-elf.sh` zatrzymuje pakowanie, jeśli którykolwiek wykonywalny payload wydania nie jest AArch64 `ET_DYN` PIE z `/system/bin/linker64`, zawiera `TEXTREL`/RPATH albo wymaga niedołączonego `libc++_shared.so`. Aplikacja powtarza podstawową kontrolę nagłówka ELF przed startem i pokazuje kod wyjścia oraz końcówkę natywnego logu zamiast ogólnego komunikatu.

CI osobno buduje i kontroluje dokładne payloady ARM64 wydania, a test inferencji, workera, parowania i hosta wykonuje na równoważnym buildzie x86_64 w sprzętowo przyspieszonym emulatorze. Hostowany runner x86_64 nie uruchamiał programowego emulatora ARM64 nawet w 30 minut, dlatego wynik x86_64 nie zastępuje testu gotowego APK na fizycznym telefonie ARM64.

Testy jednostkowe obejmują metadane GGUF, budżety, walidację kodów, granice panelu oraz rzeczywiste połączenie TLS (odrzucenie złego sekretu i certyfikatu). Opcjonalny test integracyjny:

```sh
python3 tests/tiny_model.py /tmp/yourlm-test.gguf
# Uruchom przypięty ggml-rpc-server na 127.0.0.1:50052, tylko na czas testu.
TEST_ENGINE=/sciezka/do/llama-server TEST_MODEL=/tmp/yourlm-test.gguf \
TEST_RPC=127.0.0.1:50052 go test -v -run TestRealCluster ./host
```

W środowisku budowania wykonano inferencję syntetycznego GGUF z dwoma procesami ARM64 pod QEMU, przez rzeczywisty tunel TLS, z 2 z 4 warstw na workerze. Uzyskano odpowiedź `/v1/chat/completions`; osobno sprawdzono odrzucenie zapytania bez klucza (401). **To test ścieżki wykonania, nie jakości modelu ani szybkości fizycznego klastra.**
