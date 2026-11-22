YourLM 0.2.0-rc4 — Windows x64 (natywne okno + Lenovo/Redmi bez sztucznego limitu)

Uruchom pojedynczy YourLM.exe jako zwykły użytkownik. Program otwiera panel bezpośrednio w swoim natywnym oknie WebView2; zwykła przeglądarka jest używana tylko awaryjnie, gdy WebView2 jest niedostępny. Runtime llama.cpp, minimalny ADB i aktualne APK są wbudowane w EXE oraz przygotowywane w prywatnym %AppData%\YourLM.

W zakładce Telefon USB podłącz odblokowany telefon z włączonym debugowaniem USB i użyj przycisku Zainstaluj lub zaktualizuj. `adb install -r` zachowuje prywatne dane aplikacji.

Worker Android działa jako usługa pierwszoplanowa z blokadą CPU i Wi-Fi. Po uruchomieniu może dalej pomagać laptopowi przy wyłączonym ekranie i zamkniętym widoku aplikacji; zatrzymasz go z powiadomienia lub aplikacji. Prywatny cache RPC zachowuje przydzielone tensory i znacząco przyspiesza kolejne uruchomienia tego samego modelu.

Panel pamięci pokazuje osobno pełny fizyczny RAM, użycie systemów, RAM dostępny teraz, zmapowany plik GGUF, rzeczywistą alokację silników oraz limit zatwierdzeń/pliku stronicowania Windows. Dostępne teraz nie jest limitem rozmiaru GGUF. YourLM nie odrzuca 12,4 GiB modelu statycznym planistą: host próbuje mmap, a worker używa natywnego podziału llama.cpp z rezerwą YourLM 0 MiB.

Modele, ustawienia, cache i dane aplikacji pozostają poza EXE i są zachowywane przy aktualizacji.
