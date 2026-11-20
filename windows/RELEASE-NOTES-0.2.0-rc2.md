## YourLM 0.2.0-rc2 — maksymalne użycie Lenovo + Redmi

To wydanie odpowiada na zbyt konserwatywne ograniczenie pamięci w rc1.

### Co się zmieniło

- YourLM **nie blokuje już dużego GGUF** na podstawie statycznego budżetu 2/3 lub 1/2 RAM.
- Podział wykonuje natywny mechanizm `llama.cpp --fit`: ponownie mierzy pamięć przy starcie, zapełnia wszystkie workery warstwami i pozostawia resztę modelu na hoście.
- Margines na backend został zmniejszony do 512 MiB, aby wykorzystać znacznie większą część dostępnej pamięci urządzeń.
- Host może wykorzystać mapowanie pliku GGUF i plik stronicowania Windows. Pozwala to podjąć próbę uruchomienia modelu przekraczającego aktualnie wolny RAM, kosztem dużego spadku szybkości.
- Panel pokazuje teraz **RAM dostępny w tej chwili** oraz osobne wartości dostępnej pamięci laptopa i każdego workera. Wcześniejsze 3,7 GiB było konserwatywnym progiem planisty, a nie ilością fizycznego RAM-u używaną przez nowy tryb.
- Komunikat modelu wyjaśnia różnicę pomiędzy rozmiarem GGUF, aktualnie wolnym RAM-em i próbą maksymalną.

### Ważne

Tryb maksymalny nie tworzy dodatkowej pamięci. Windows, HyperOS, KV cache i bufory nadal jej potrzebują. Gemma 4 26B-A4B o rozmiarze 12,4 GiB zostanie podjęta i automatycznie podzielona, ale może ładować się długo, intensywnie korzystać z pliku stronicowania albo zostać zatrzymana przez system. A4B oznacza aktywnych ekspertów na token, nie tylko 4B wag przechowywanych w pamięci.

Aktualizację można zainstalować z poziomu rc1. Modele, ustawienia i prywatne dane pozostają na miejscu.
