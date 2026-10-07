import io
import unittest

from PIL import Image
from recognizer import decode_photo, FaceError


class DecodeTests(unittest.TestCase):
    def test_jpeg_and_png(self):
        for format in ("JPEG", "PNG"):
            with self.subTest(format=format):
                stream = io.BytesIO()
                Image.new("RGB", (64, 48)).save(stream, format=format)
                self.assertEqual(decode_photo(stream.getvalue()).shape, (48, 64, 3))

    def test_invalid_small_and_oversized_images(self):
        images = [b"", b"not an image", bytes(2 * 1024 * 1024 + 1)]
        for size in ((1, 1), (2100, 2100)):
            stream = io.BytesIO()
            Image.new("RGB", size).save(stream, format="PNG")
            images.append(stream.getvalue())
        for data in images:
            with self.assertRaises(FaceError) as error:
                decode_photo(data)
            self.assertEqual(error.exception.status, "invalid_image")

    def test_exif_orientation(self):
        stream = io.BytesIO()
        image = Image.new("RGB", (64, 48))
        exif = image.getexif()
        exif[274] = 6
        image.save(stream, format="JPEG", exif=exif)
        self.assertEqual(decode_photo(stream.getvalue()).shape, (64, 48, 3))


if __name__ == "__main__":
    unittest.main()
