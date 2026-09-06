import { crc32 } from 'node:zlib';
import { writeFileSync } from 'node:fs';
import { PNG } from 'pngjs';
const signature=Buffer.from([137,80,78,71,13,10,26,10]);
function chunk(type,data=Buffer.alloc(0)){const b=Buffer.alloc(data.length+12);b.writeUInt32BE(data.length);b.write(type,4);data.copy(b,8);b.writeUInt32BE(crc32(b.subarray(4,-4)),b.length-4);return b;}
function header(width=2,height=2,depth=8,color=6,interlace=0){const b=Buffer.alloc(13);b.writeUInt32BE(width);b.writeUInt32BE(height,4);b[8]=depth;b[9]=color;b[12]=interlace;return Buffer.concat([signature,chunk('IHDR',b)]);}
const fixtures=[];const add=(name,data,status=200)=>fixtures.push({name,base64:data.toString('base64'),status});
const png=new PNG({width:2,height:2});png.data.fill(255);add('complete-rgba',PNG.sync.write(png));
for(const [color,depths] of [[0,[1,2,4,8,16]],[2,[8,16]],[4,[8,16]],[6,[8,16]]])for(const depth of depths)add(`header-color-${color}-depth-${depth}`,header(2,2,depth,color));
for(const depth of [1,2,4,8])add(`palette-${depth}`,Buffer.concat([header(2,2,depth,3),chunk('PLTE',Buffer.from([0,0,0])),chunk('IDAT')]));
add('palette-alpha-extended',Buffer.concat([header(2,2,1,3),chunk('PLTE',Buffer.from([0,0,0])),chunk('tRNS',Buffer.alloc(4,255))]));
add('interlaced-header',header(2,2,16,6,1));add('maximum-dimensions',header(4096,4096));
add('zero-width',header(0,2),400);add('too-wide',header(4097,2),400);add('too-tall',header(2,4097),400);add('wrong-color-depth',header(2,2,1,6),400);add('wrong-interlace',header(2,2,8,6,2),400);
add('palette-missing',header(2,2,8,3),400);add('palette-invalid-size',Buffer.concat([header(2,2,1,3),chunk('PLTE',Buffer.alloc(9)),chunk('IDAT')]),400);
const corrupt=header();corrupt[corrupt.length-1]^=1;add('bad-header-crc',corrupt,400);add('truncated',header().subarray(0,20),400);add('non-png',Buffer.from('not an image'),400);add('empty',Buffer.alloc(0),413);
const highBitHeader=header();highBitHeader[12]|=0x80;highBitHeader.writeUInt32BE(crc32(highBitHeader.subarray(12,-4)),highBitHeader.length-4);add('high-bit-header-name',highBitHeader,400);
const highBitData=chunk('IDAT');highBitData[4]|=0x80;highBitData.writeUInt32BE(crc32(highBitData.subarray(4,-4)),highBitData.length-4);add('high-bit-idat-name',Buffer.concat([header(2,2,8,3),chunk('PLTE',Buffer.from([0,0,0])),highBitData]),400);
add('unknown-before-header',Buffer.concat([signature,chunk('mist',Buffer.from('metadata')),header().subarray(8)]));
add('indexed-idat-header-only',Buffer.concat([header(2,2,8,3),chunk('PLTE',Buffer.from([0,0,0])),chunk('IDAT').subarray(0,8)]));
writeFileSync(new URL('../../docs/migration/fixtures/avatar-png.json',import.meta.url),JSON.stringify(fixtures,null,2)+'\n');
