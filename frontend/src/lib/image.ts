/** Centre-crop an image file to a square of `size` px and return it as a JPEG data URL. */
export async function resizeToDataUrl(file: File, size: number): Promise<string> {
	if (!file.type.startsWith('image/')) throw new Error('Choose an image file');
	const bitmap = await createImageBitmap(file).catch(() => {
		throw new Error('Could not read that image');
	});
	const side = Math.min(bitmap.width, bitmap.height);
	const canvas = document.createElement('canvas');
	canvas.width = canvas.height = size;
	const ctx = canvas.getContext('2d');
	if (!ctx) throw new Error('Could not process the image');
	ctx.drawImage(bitmap, (bitmap.width - side) / 2, (bitmap.height - side) / 2, side, side, 0, 0, size, size);
	bitmap.close();
	return canvas.toDataURL('image/jpeg', 0.85);
}
