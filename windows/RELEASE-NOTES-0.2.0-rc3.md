## YourLM 0.2.0-rc3 — duży GGUF bez limitu planisty

To wydanie usuwa ostatnią blokadę, która mogła zatrzymać model większy od fizycznego RAM-u laptopa zanim Windows zdążył użyć mapowania pliku.

### Co zmieniono

- Stary statyczny planista i jego progi pamięci zostały całkowicie usunięte z uruchamiania oraz interfejsu.
- Bez workera YourLM uruchamia CPU z `--fit off`. Dzięki temu `llama.cpp` nie odrzuca modelu na podstawie samego fizycznego RAM-u, a Windows może spróbować obsłużyć file-backed mmap bezpośrednio z pliku GGUF.
- Z workerami pozostaje natywny, tensorowy podział `llama.cpp --fit`, ale `--fit-target 0` usuwa dodatkową rezerwę YourLM i wykorzystuje całe wolne miejsce zgłaszane przez workery.
- YourLM nadal nie przypina `-ngl` ani `--tensor-split`; rzeczywiste rozmieszczenie wybiera runtime po pomiarze urządzeń.
- Ekran pokazuje pamięć fizyczną, aktualnie dostępną i zerową sztuczną rezerwę oraz ma jednoznaczny przycisk **Uruchom model bez limitu**.

### Gemma 4 26B-A4B 12,4 GiB

YourLM podejmie teraz próbę uruchomienia tego pliku zarówno bez workera, jak i z podłączonym Redmi. Ładowanie z mmap może być bardzo wolne i intensywnie czytać z dysku. Brak blokady aplikacji nie zmienia fizycznej ilości RAM-u: jeśli Windows lub Android rzeczywiście wyczerpie pamięć potrzebną także na bufory i działanie systemu, proces może zostać przerwany.

Aktualizacja z rc2 zachowuje modele, ustawienia i dane aplikacji.
