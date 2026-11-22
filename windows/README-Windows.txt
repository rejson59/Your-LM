YourLM 0.2.0-rc10 — Windows x64 (rezerwa szczytu według największego tensora + telemetria pamięci Androida)

Uruchom pobrany pojedynczy YourLM.exe jako zwykły użytkownik. Przy pierwszym starcie program instaluje się bez administratora w %LocalAppData%\Programs\YourLM, dodaje skrót do Menu Start i wpis w Ustawienia → Aplikacje → Zainstalowane aplikacje, a następnie otwiera panel bezpośrednio w natywnym oknie WebView2. Zwykła przeglądarka jest używana tylko awaryjnie, gdy WebView2 jest niedostępny. Runtime llama.cpp, minimalny ADB i aktualne APK są wbudowane w EXE oraz przygotowywane w prywatnym %AppData%\YourLM. Parametr --portable pomija instalację.

W zakładce Telefon USB podłącz odblokowany telefon z włączonym debugowaniem USB i użyj przycisku Zainstaluj lub zaktualizuj. `adb install -r` zachowuje prywatne dane aplikacji. Po każdej aktualizacji EXE wykonaj ten krok ponownie — aplikacja Windows nie podmienia samoczynnie APK już zainstalowanego na telefonie.

Worker Android działa jako usługa pierwszoplanowa z blokadą CPU i Wi-Fi. Po uruchomieniu może dalej pomagać laptopowi przy wyłączonym ekranie i zamkniętym widoku aplikacji; zatrzymasz go z powiadomienia lub aplikacji. Prywatny cache RPC zachowuje przydzielone tensory i znacząco przyspiesza kolejne uruchomienia tego samego modelu. Cache jest strumieniowany porcjami do 16 MiB i odrzuca wpisy niepełne lub uszkodzone, zamiast tworzyć w RAM dodatkową kopię całego tensora. RC10 pobiera z Redmi migawkę diagnostyczną co 3 sekundy podczas ładowania. Jeśli po awarii listener telefonu zniknie, jeden raport kopiowany w Windows zawiera ostatnią udaną migawkę, w tym błędy mostu Java i końcówkę szczegółowego logu natywnego.

Panel pamięci pokazuje osobno pełny fizyczny RAM, użycie systemów, RAM dostępny teraz, zmapowany plik GGUF, rzeczywistą alokację silników oraz limit zatwierdzeń/pliku stronicowania Windows. Dostępne teraz nie jest limitem rozmiaru GGUF. YourLM nie odrzuca 12,4 GiB modelu statycznym planistą: host próbuje mmap, a worker używa natywnego podziału llama.cpp z rezerwą szczytu ładowania wyliczaną z największego tensora (minimum 2048 MiB) dla Androida i RPC.

Podczas startu modelu panel pokazuje potwierdzony etap, czas oraz rzeczywistą aktywność dysku, CPU i RPC. Etapy są orientacyjne, a nie udawanym procentem tensorów. Po dłuższym braku wszystkich mierzalnych sygnałów panel podpowiada, kiedy rozsądnie zatrzymać próbę.

Modele, ustawienia, cache i dane aplikacji pozostają poza EXE i są zachowywane przy aktualizacji.
